package com.dozzy.bridge;

import com.dozzy.BindWAPlugin;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import net.md_5.bungee.api.chat.ClickEvent;
import net.md_5.bungee.api.chat.ComponentBuilder;
import net.md_5.bungee.api.chat.HoverEvent;
import net.md_5.bungee.api.chat.TextComponent;
import org.bukkit.Bukkit;
import org.bukkit.OfflinePlayer;
import org.bukkit.Sound;
import org.bukkit.entity.Player;
import org.java_websocket.client.WebSocketClient;
import org.java_websocket.handshake.ServerHandshake;

import java.net.URI;
import java.util.Optional;
import java.util.UUID;
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
            } else if ("imagemap_paid".equals(type)) {
                String orderId = json.has("order_id") ? json.get("order_id").getAsString() : "";
                String mapName = json.has("map_name") ? json.get("map_name").getAsString() : "";
                int width = json.has("width") ? json.get("width").getAsInt() : 1;
                int height = json.has("height") ? json.get("height").getAsInt() : 1;
                String senderPhone = json.has("sender_phone") ? json.get("sender_phone").getAsString() : "";
                String imageUrl = json.has("image_url") ? json.get("image_url").getAsString() : "";

                if (imageUrl.startsWith("/")) {
                    String base = plugin.getPluginConfig().getApiBaseUrl().trim();
                    if (base.endsWith("/")) {
                        base = base.substring(0, base.length() - 1);
                    }
                    imageUrl = base + imageUrl;
                }

                final String finalImageUrl = imageUrl;
                plugin.getLogger().info("[ImageMap] Menerima order terbayar: " + orderId + " (Map: " + mapName + ", " + width + "x" + height + ")");

                // Cek apakah nomor WA pengorder sudah terhubung ke akun Minecraft
                com.dozzy.database.DatabaseManager db = plugin.getDatabaseManager();
                Optional<com.dozzy.database.WABinding> bindingOpt = db.getBindingByPhone(senderPhone);

                if (bindingOpt.isPresent() && bindingOpt.get().isVerified()) {
                    UUID uuid = bindingOpt.get().getUuid();
                    OfflinePlayer offP = Bukkit.getOfflinePlayer(uuid);
                    String playerName = (offP != null && offP.getName() != null) ? offP.getName() : db.getPlayerNameByUuid(uuid);

                    db.saveImageMapClaim(orderId, mapName, playerName, senderPhone, finalImageUrl, width, height);

                    // Eksekusi pembuatan map di ImageFrame dengan menyertakan prefix player (wajib untuk console)
                    Bukkit.getScheduler().runTask(plugin, () -> {
                        String createCmd = plugin.getPluginConfig().buildImageMapCreateCommand(playerName, mapName, finalImageUrl, width, height);
                        plugin.getLogger().info("[ImageMap] Menjalankan perintah pembuatan map: " + createCmd);
                        Bukkit.dispatchCommand(Bukkit.getConsoleSender(), createCmd);
                    });

                    // Beritahu player jika sedang online di server
                    Bukkit.getScheduler().runTask(plugin, () -> {
                        Player onlineP = Bukkit.getPlayer(uuid);
                        if (onlineP != null && onlineP.isOnline()) {
                            onlineP.sendMessage("§a[ImageMap] Pemesanan poster '§e" + mapName + "§a' berhasil diproses!");
                            onlineP.sendMessage("§aGunakan command §b/getimagemap §auntuk mengambil item peta ke inventory!");
                            onlineP.playSound(onlineP.getLocation(), Sound.ENTITY_PLAYER_LEVELUP, 1.0f, 1.0f);
                        }
                    });

                    // Kirim status kembali ke bot WA bahwa player sudah terikat
                    JsonObject res = new JsonObject();
                    res.addProperty("type", "imagemap_order_status");
                    res.addProperty("order_id", orderId);
                    res.addProperty("bound", true);
                    res.addProperty("player_name", playerName);
                    send(res.toString());
                } else {
                    // Nomor WA belum terikat ke akun Minecraft, simpan pending claim dan minta bot WA menanyakan username
                    db.saveImageMapClaim(orderId, mapName, null, senderPhone, finalImageUrl, width, height);

                    JsonObject res = new JsonObject();
                    res.addProperty("type", "imagemap_order_status");
                    res.addProperty("order_id", orderId);
                    res.addProperty("bound", false);
                    send(res.toString());
                }
            } else if ("imagemap_assign_player".equals(type)) {
                String orderId = json.has("order_id") ? json.get("order_id").getAsString() : "";
                String mapName = json.has("map_name") ? json.get("map_name").getAsString() : "";
                String playerName = json.has("player_name") ? json.get("player_name").getAsString() : "";
                String imageUrl = json.has("image_url") ? json.get("image_url").getAsString() : "";
                int width = json.has("width") ? json.get("width").getAsInt() : 1;
                int height = json.has("height") ? json.get("height").getAsInt() : 1;

                if (imageUrl.startsWith("/")) {
                    String base = plugin.getPluginConfig().getApiBaseUrl().trim();
                    if (base.endsWith("/")) {
                        base = base.substring(0, base.length() - 1);
                    }
                    imageUrl = base + imageUrl;
                }

                com.dozzy.database.DatabaseManager db = plugin.getDatabaseManager();
                if (imageUrl.isEmpty()) {
                    Optional<com.dozzy.database.ImageMapClaim> existing = db.getClaimByOrderId(orderId);
                    if (existing.isPresent()) {
                        if (imageUrl.isEmpty() && existing.get().getImageUrl() != null) {
                            imageUrl = existing.get().getImageUrl();
                        }
                        if (width <= 1 && existing.get().getWidth() > 0) {
                            width = existing.get().getWidth();
                        }
                        if (height <= 1 && existing.get().getHeight() > 0) {
                            height = existing.get().getHeight();
                        }
                    }
                }

                final String finalImageUrl = imageUrl;
                final int finalWidth = width;
                final int finalHeight = height;

                plugin.getLogger().info("[ImageMap] Menetapkan player " + playerName + " untuk order: " + orderId);
                db.assignPlayerToClaim(orderId, playerName, finalImageUrl, finalWidth, finalHeight);

                // Eksekusi pembuatan map di ImageFrame sekarang karena player sudah diketahui
                if (!finalImageUrl.isEmpty()) {
                    Bukkit.getScheduler().runTask(plugin, () -> {
                        String createCmd = plugin.getPluginConfig().buildImageMapCreateCommand(playerName, mapName, finalImageUrl, finalWidth, finalHeight);
                        plugin.getLogger().info("[ImageMap] Menjalankan perintah pembuatan map untuk player " + playerName + ": " + createCmd);
                        Bukkit.dispatchCommand(Bukkit.getConsoleSender(), createCmd);
                    });
                }

                Bukkit.getScheduler().runTask(plugin, () -> {
                    Player onlineP = Bukkit.getPlayerExact(playerName);
                    if (onlineP == null) {
                        onlineP = Bukkit.getPlayer(playerName);
                    }
                    if (onlineP != null && onlineP.isOnline()) {
                        onlineP.sendMessage("§a[ImageMap] Pemesanan poster '§e" + mapName + "§a' telah dihubungkan ke akun Anda!");
                        onlineP.sendMessage("§aGunakan command §b/getimagemap §auntuk mengambil item peta ke inventory!");
                        onlineP.playSound(onlineP.getLocation(), Sound.ENTITY_PLAYER_LEVELUP, 1.0f, 1.0f);
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
