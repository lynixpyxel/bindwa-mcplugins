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
