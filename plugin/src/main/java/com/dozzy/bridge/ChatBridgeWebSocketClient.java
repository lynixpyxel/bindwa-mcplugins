package com.dozzy.bridge;

import com.dozzy.BindWAPlugin;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import net.md_5.bungee.api.chat.ClickEvent;
import net.md_5.bungee.api.chat.ComponentBuilder;
import net.md_5.bungee.api.chat.HoverEvent;
import net.md_5.bungee.api.chat.TextComponent;
import org.bukkit.Bukkit;
import org.bukkit.entity.Player;
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
                String msgId = json.has("msg_id") ? json.get("msg_id").getAsString() : "";
                String groupJid = json.has("group") ? json.get("group").getAsString() : "";
                String groupName = json.has("group_name") ? json.get("group_name").getAsString() : "Grup WA";
                String pushName = json.has("push_name") ? json.get("push_name").getAsString() : "Anon";
                String sender = json.has("sender") ? json.get("sender").getAsString() : "";
                String senderJid = json.has("sender_jid") ? json.get("sender_jid").getAsString() : "";
                String text = json.has("text") ? json.get("text").getAsString() : "";
                String quotedAuthor = json.has("quoted_author") ? json.get("quoted_author").getAsString() : "";
                String quotedText = json.has("quoted_text") ? json.get("quoted_text").getAsString() : "";

                // Simpan ke cache untuk fitur Reply
                WAMessageContext ctx = manager.saveIncomingMessage(msgId, groupJid, groupName, sender, senderJid, pushName, text);

                // Format tampilan chat di Minecraft
                StringBuilder sb = new StringBuilder();
                sb.append("§b|§a").append(groupName).append("§b| <§a").append(pushName).append("§b>");

                if (!quotedAuthor.isEmpty()) {
                    sb.append(" §8[↳ §7@").append(quotedAuthor);
                    if (!quotedText.isEmpty()) {
                        sb.append(": §o\"").append(quotedText).append("\"§7");
                    }
                    sb.append("§8]");
                }

                sb.append("§b:§r ").append(text);

                String formattedText = sb.toString();

                plugin.getLogger().info("[WA -> MC] [" + groupName + "] " + pushName + (quotedAuthor.isEmpty() ? "" : " (Replying @" + quotedAuthor + ")") + ": " + text);

                Bukkit.getScheduler().runTask(plugin, () -> {
                    TextComponent mainComponent = new TextComponent(formattedText);

                    // Tambahkan tombol interaktif [Reply]
                    TextComponent replyBtn = new TextComponent(" §e[Reply]");
                    replyBtn.setBold(true);
                    replyBtn.setHoverEvent(new HoverEvent(HoverEvent.Action.SHOW_TEXT,
                            new ComponentBuilder("§aKlik untuk membalas pesan WhatsApp dari §e@" + pushName).create()));
                    replyBtn.setClickEvent(new ClickEvent(ClickEvent.Action.SUGGEST_COMMAND,
                            "/chat reply " + ctx.getShortId() + " "));

                    mainComponent.addExtra(replyBtn);

                    for (Player player : Bukkit.getOnlinePlayers()) {
                        player.spigot().sendMessage(mainComponent);
                    }
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
