package com.dozzy.bridge;

import com.dozzy.BindWAPlugin;
import com.dozzy.config.PluginConfig;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import org.bukkit.Bukkit;
import org.java_websocket.client.WebSocketClient;
import org.java_websocket.handshake.ServerHandshake;

import java.net.URI;
import java.util.logging.Level;

public class ChatBridgeWebSocketClient extends WebSocketClient {

    private final BindWAPlugin plugin;
    private final ChatBridgeManager manager;

    public ChatBridgeWebSocketClient(BindWAPlugin plugin, ChatBridgeManager manager, URI serverUri) {
        super(serverUri);
        this.plugin = plugin;
        this.manager = manager;
    }

    @Override
    public void onOpen(ServerHandshake handshakedata) {
        plugin.getLogger().info("[Chat-Bridge] Berhasil tersambung ke WebSocket Bot WhatsApp di " + getURI());
        manager.sendServerHeartbeat();
    }

    @Override
    public void onMessage(String message) {
        try {
            JsonObject json = JsonParser.parseString(message).getAsJsonObject();
            if (!json.has("type")) {
                return;
            }

            String type = json.get("type").getAsString();
            if ("chat_wa".equals(type)) {
                String pushName = json.has("push_name") ? json.get("push_name").getAsString() : "Anon";
                String sender = json.has("sender") ? json.get("sender").getAsString() : "";
                String text = json.has("text") ? json.get("text").getAsString() : "";

                String formattedMsg = "§b|§aGrup WA§b| <§a" + pushName + "§b> (§a" + sender + "§b):§r " + text
                        + "\n§6§otulis '/chat <pesan>' untuk membalas ke grup WhatsApp";

                plugin.getLogger().info("[WA -> MC] " + pushName + ": " + text);

                Bukkit.getScheduler().runTask(plugin, () -> {
                    Bukkit.broadcastMessage(formattedMsg);
                });
            }

        } catch (Exception e) {
            plugin.getLogger().log(Level.WARNING, "[Chat-Bridge] Gagal memproses pesan WS dari bot: " + e.getMessage());
        }
    }

    @Override
    public void onClose(int code, String reason, boolean remote) {
        plugin.getLogger().info("[Chat-Bridge] Terputus dari WebSocket Bot WhatsApp: " + reason + " (Code: " + code + ")");
        manager.scheduleReconnect();
    }

    @Override
    public void onError(Exception ex) {
        plugin.getLogger().log(Level.WARNING, "[Chat-Bridge] Error pada WebSocket Bot WhatsApp: " + ex.getMessage());
    }
}
