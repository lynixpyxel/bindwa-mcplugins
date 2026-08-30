package com.dozzy.bridge;

import com.dozzy.BindWAPlugin;
import com.dozzy.config.PluginConfig;
import com.dozzy.http.BotApiClient;
import com.google.gson.JsonArray;
import com.google.gson.JsonObject;
import org.bukkit.Bukkit;
import org.bukkit.entity.Player;
import org.bukkit.scheduler.BukkitTask;

import java.net.URI;
import java.util.Collection;
import java.util.List;
import java.util.logging.Level;
import java.util.stream.Collectors;

public class ChatBridgeManager {

    private final BindWAPlugin plugin;
    private final PluginConfig config;
    private final BotApiClient apiClient;

    private final java.util.Map<String, WAMessageContext> messageCache = new java.util.concurrent.ConcurrentHashMap<>();
    private final java.util.LinkedList<String> cacheOrder = new java.util.LinkedList<>();
    private static final int MAX_CACHE_SIZE = 100;
    private final java.util.Set<String> knownWaUsers = java.util.concurrent.ConcurrentHashMap.newKeySet();

    private ChatBridgeWebSocketClient wsClient;
    private BukkitTask reconnectTask;
    private BukkitTask heartbeatTask;
    private boolean isRunning = false;

    public ChatBridgeManager(BindWAPlugin plugin, PluginConfig config, BotApiClient apiClient) {
        this.plugin = plugin;
        this.config = config;
        this.apiClient = apiClient;
    }

    public synchronized void start() {
        if (!config.isChatBridgeEnabled()) {
            plugin.getLogger().info("[Chat-Bridge] Dinonaktifkan di config.yml.");
            return;
        }

        this.isRunning = true;

        // 1. Sambungkan WebSocket Client ke Bot WhatsApp
        connectWebSocket();

        // 2. Jalankan Heartbeat status server setiap 10 detik
        startHeartbeat();
    }

    public synchronized void connectWebSocket() {
        if (!isRunning || !config.isChatBridgeEnabled()) {
            return;
        }

        try {
            String baseUrl = config.getApiBaseUrl().trim();
            String wsUrl;
            if (baseUrl.startsWith("https://")) {
                wsUrl = "wss://" + baseUrl.substring(8);
            } else if (baseUrl.startsWith("http://")) {
                wsUrl = "ws://" + baseUrl.substring(7);
            } else {
                wsUrl = "ws://" + baseUrl;
            }

            if (wsUrl.endsWith("/")) {
                wsUrl = wsUrl.substring(0, wsUrl.length() - 1);
            }
            wsUrl += "/ws?token=" + config.getApiToken();

            URI uri = new URI(wsUrl);
            wsClient = new ChatBridgeWebSocketClient(plugin, this, uri);
            wsClient.connect();
            plugin.getLogger().info("[Chat-Bridge] Menghubungkan ke WebSocket Bot WhatsApp di " + wsUrl);

        } catch (Exception e) {
            plugin.getLogger().log(Level.WARNING, "[Chat-Bridge] Gagal membuat koneksi WS ke Bot: " + e.getMessage());
            scheduleReconnect();
        }
    }

    public synchronized void scheduleReconnect() {
        if (!isRunning || !config.isChatBridgeEnabled()) {
            return;
        }

        if (reconnectTask != null && !reconnectTask.isCancelled()) {
            return;
        }

        reconnectTask = Bukkit.getScheduler().runTaskLaterAsynchronously(plugin, () -> {
            reconnectTask = null;
            if (isRunning && (wsClient == null || !wsClient.isOpen())) {
                plugin.getLogger().info("[Chat-Bridge] Mencoba reconnect ke WebSocket Bot WhatsApp...");
                connectWebSocket();
            }
        }, 200L); // 10 detik
    }

    private void startHeartbeat() {
        if (heartbeatTask != null) {
            heartbeatTask.cancel();
        }

        // Kirim status server setiap 10 detik
        heartbeatTask = Bukkit.getScheduler().runTaskTimerAsynchronously(plugin, this::sendServerHeartbeat, 40L, 200L);
    }

    public void sendServerHeartbeat() {
        if (!isRunning) {
            return;
        }

        Bukkit.getScheduler().runTask(plugin, () -> {
            Collection<? extends Player> onlinePlayers = Bukkit.getOnlinePlayers();
            int playerCount = onlinePlayers.size();
            int maxPlayers = Bukkit.getMaxPlayers();
            List<String> playerList = onlinePlayers.stream()
                    .map(Player::getName)
                    .collect(Collectors.toList());

            double[] tpsArr = Bukkit.getTPS();
            double tps = tpsArr.length > 0 ? Math.min(20.0, tpsArr[0]) : 20.0;

            // 1. Kirim via WebSocket jika terbuka
            if (wsClient != null && wsClient.isOpen()) {
                JsonObject obj = new JsonObject();
                obj.addProperty("type", "status_heartbeat");
                obj.addProperty("player_count", playerCount);
                obj.addProperty("max_players", maxPlayers);
                obj.addProperty("tps", tps);
                JsonArray arr = new JsonArray();
                for (String p : playerList) {
                    arr.add(p);
                }
                obj.add("player_list", arr);
                wsClient.send(obj.toString());
            } else {
                // 2. Fallback kirim via HTTP POST jika WebSocket belum open
                apiClient.sendServerStatus(playerCount, maxPlayers, playerList, tps);
            }
        });
    }

    public boolean sendChatMessage(String playerName, String message) {
        String formatted = "§b|§6MC ➔ WA§b| <§f" + playerName + "§b>:§r " + message;
        Bukkit.getScheduler().runTask(plugin, () -> Bukkit.broadcastMessage(formatted));
        plugin.getLogger().info("[MC -> WA] " + playerName + ": " + message);

        if (wsClient != null && wsClient.isOpen()) {
            JsonObject obj = new JsonObject();
            obj.addProperty("type", "chat");
            obj.addProperty("player", playerName);
            obj.addProperty("message", message);
            wsClient.send(obj.toString());
            return true;
        }

        // Fallback HTTP
        apiClient.sendGroupChat(playerName, message);
        return true;
    }

    public synchronized WAMessageContext saveIncomingMessage(String msgId, String groupJid, String groupName, String senderPhone, String senderJid, String pushName, String text) {
        String cleanMsgId = (msgId != null && !msgId.isEmpty()) ? msgId : java.util.UUID.randomUUID().toString();
        String shortId = cleanMsgId;
        if (cleanMsgId.length() > 6) {
            shortId = cleanMsgId.substring(cleanMsgId.length() - 6);
        }

        WAMessageContext ctx = new WAMessageContext(shortId, cleanMsgId, groupJid, groupName, senderPhone, senderJid, pushName, text);

        if (pushName != null && !pushName.trim().isEmpty()) {
            knownWaUsers.add(pushName.trim().replaceAll("\\s+", ""));
        }
        if (senderPhone != null && !senderPhone.trim().isEmpty()) {
            knownWaUsers.add(senderPhone.trim());
        }

        messageCache.put(shortId.toLowerCase(), ctx);
        messageCache.put(cleanMsgId.toLowerCase(), ctx);

        cacheOrder.add(shortId.toLowerCase());
        cacheOrder.add(cleanMsgId.toLowerCase());
        while (cacheOrder.size() > MAX_CACHE_SIZE * 2) {
            String oldest = cacheOrder.removeFirst();
            messageCache.remove(oldest);
        }

        return ctx;
    }

    public java.util.List<String> getKnownWaUsers() {
        return new java.util.ArrayList<>(knownWaUsers);
    }

    public WAMessageContext getMessageContext(String id) {
        if (id == null) return null;
        return messageCache.get(id.toLowerCase().trim());
    }

    public synchronized java.util.List<WAMessageContext> getRecentMessages(int limit) {
        java.util.List<WAMessageContext> list = new java.util.ArrayList<>();
        java.util.Set<String> seen = new java.util.HashSet<>();
        for (int i = cacheOrder.size() - 1; i >= 0; i--) {
            String key = cacheOrder.get(i);
            WAMessageContext ctx = messageCache.get(key);
            if (ctx != null && seen.add(ctx.getFullMsgId())) {
                list.add(ctx);
                if (list.size() >= limit) {
                    break;
                }
            }
        }
        return list;
    }

    public boolean sendReplyMessage(String playerName, String message, WAMessageContext context) {
        StringBuilder sb = new StringBuilder();
        sb.append("§b|§6MC ➔ WA§b| <§f").append(playerName).append("§b>");
        if (context != null) {
            sb.append(" §8[↳ §7@").append(context.getPushName());
            String quotedSnippet = context.getText();
            if (quotedSnippet.length() > 30) {
                quotedSnippet = quotedSnippet.substring(0, 30) + "...";
            }
            sb.append(": §o\"").append(quotedSnippet).append("\"§7§8]");
        }
        sb.append("§b:§r ").append(message);

        String formatted = sb.toString();
        Bukkit.getScheduler().runTask(plugin, () -> Bukkit.broadcastMessage(formatted));
        plugin.getLogger().info("[MC -> WA] " + playerName + (context != null ? " (Replying @" + context.getPushName() + ")" : "") + ": " + message);

        if (wsClient != null && wsClient.isOpen()) {
            JsonObject obj = new JsonObject();
            obj.addProperty("type", "chat");
            obj.addProperty("player", playerName);
            obj.addProperty("message", message);
            if (context != null) {
                obj.addProperty("reply_to_id", context.getFullMsgId());
                obj.addProperty("reply_group", context.getGroupJid());
                obj.addProperty("reply_sender", context.getSenderJid());
                obj.addProperty("quoted_text", context.getText());
            }
            wsClient.send(obj.toString());
            return true;
        }

        // Fallback HTTP
        if (context != null) {
            apiClient.sendGroupReply(playerName, message, context.getFullMsgId(), context.getGroupJid(), context.getSenderJid(), context.getText());
        } else {
            apiClient.sendGroupChat(playerName, message);
        }
        return true;
    }

    public boolean sendNotification(String title, String message) {
        if (wsClient != null && wsClient.isOpen()) {
            JsonObject obj = new JsonObject();
            obj.addProperty("type", "notif");
            obj.addProperty("title", title);
            obj.addProperty("message", message);
            wsClient.send(obj.toString());
            return true;
        }

        // Fallback HTTP
        apiClient.sendNotification(title, message);
        return true;
    }

    public synchronized void stop() {
        this.isRunning = false;

        if (heartbeatTask != null) {
            heartbeatTask.cancel();
            heartbeatTask = null;
        }

        if (reconnectTask != null) {
            reconnectTask.cancel();
            reconnectTask = null;
        }

        if (wsClient != null) {
            try {
                wsClient.close();
            } catch (Exception ignored) {
            }
            wsClient = null;
        }

        plugin.getLogger().info("[Chat-Bridge] Layanan Chat Bridge dimatikan.");
    }

    public boolean isConnected() {
        return wsClient != null && wsClient.isOpen();
    }
}
