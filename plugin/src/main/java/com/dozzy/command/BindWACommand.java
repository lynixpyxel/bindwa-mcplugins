package com.dozzy.command;

import com.dozzy.BindWAPlugin;
import com.dozzy.config.PluginConfig;
import com.dozzy.database.DatabaseManager;
import com.dozzy.database.WABinding;
import com.dozzy.service.BindingService;
import com.dozzy.ui.BedrockFormFlow;
import com.dozzy.ui.JavaAnvilFlow;
import org.bukkit.Bukkit;
import org.bukkit.command.Command;
import org.bukkit.command.CommandExecutor;
import org.bukkit.command.CommandSender;
import org.bukkit.entity.Player;
import org.jetbrains.annotations.NotNull;

import java.util.Map;
import java.util.Optional;
import java.util.UUID;

public class BindWACommand implements CommandExecutor {

    private final BindWAPlugin plugin;
    private final BindingService bindingService;
    private final JavaAnvilFlow javaAnvilFlow;
    private final BedrockFormFlow bedrockFormFlow;

    public BindWACommand(BindWAPlugin plugin, BindingService bindingService,
                         JavaAnvilFlow javaAnvilFlow, BedrockFormFlow bedrockFormFlow) {
        this.plugin = plugin;
        this.bindingService = bindingService;
        this.javaAnvilFlow = javaAnvilFlow;
        this.bedrockFormFlow = bedrockFormFlow;
    }

    @Override
    public boolean onCommand(@NotNull CommandSender sender, @NotNull Command command, @NotNull String label, @NotNull String[] args) {
        if (args.length > 0) {
            if (args[0].equalsIgnoreCase("reload")) {
                if (!sender.hasPermission("bindwa.admin")) {
                    sender.sendMessage(PluginConfig.colorize("&cKamu tidak memiliki izin untuk menggunakan perintah ini."));
                    return true;
                }
                plugin.reloadPluginConfig();
                sender.sendMessage(PluginConfig.colorize("&a[BindWA] Konfigurasi berhasil di-reload!"));
                return true;
            }

            if (args[0].equalsIgnoreCase("testnotif")) {
                if (!sender.hasPermission("bindwa.admin")) {
                    sender.sendMessage(PluginConfig.colorize("&cKamu tidak memiliki izin untuk menggunakan perintah ini."));
                    return true;
                }
                if (args.length < 2) {
                    sender.sendMessage(PluginConfig.colorize("&cPenggunaan: /bindwa testnotif <dragon|elytra|egg>"));
                    return true;
                }
                String senderName = (sender instanceof Player p) ? p.getName() : "Admin";
                String type = args[1].toLowerCase();

                switch (type) {
                    case "dragon", "enderdragon" -> {
                        String msg = "🐉 *Ender Dragon* telah berhasil dikalahkan oleh *" + senderName + "*! (Test)";
                        plugin.getChatBridgeManager().sendNotification("Ender Dragon Defeated", msg);
                        sender.sendMessage(PluginConfig.colorize("&a[BindWA] Test notifikasi Ender Dragon telah dikirim ke grup WhatsApp!"));
                    }
                    case "elytra" -> {
                        String msg = "🪽 *" + senderName + "* berhasil mendapatkan *Elytra* di The End! (Test)";
                        plugin.getChatBridgeManager().sendNotification("Elytra Obtained", msg);
                        sender.sendMessage(PluginConfig.colorize("&a[BindWA] Test notifikasi Elytra telah dikirim ke grup WhatsApp!"));
                    }
                    case "egg" -> {
                        String msg = "🥚 *Dragon Egg* telah diambil/disentuh oleh *" + senderName + "*! (Test)";
                        plugin.getChatBridgeManager().sendNotification("Dragon Egg Taken", msg);
                        sender.sendMessage(PluginConfig.colorize("&a[BindWA] Test notifikasi Dragon Egg telah dikirim ke grup WhatsApp!"));
                    }
                    default -> sender.sendMessage(PluginConfig.colorize("&cPilihan tidak valid: pilih 'dragon', 'elytra', atau 'egg'."));
                }
                return true;
            }

            if (args[0].equalsIgnoreCase("unbind") || args[0].equalsIgnoreCase("reset")) {
                if (!sender.hasPermission("bindwa.admin")) {
                    sender.sendMessage(PluginConfig.colorize("&cKamu tidak memiliki izin untuk menggunakan perintah ini."));
                    return true;
                }
                if (args.length < 2) {
                    sender.sendMessage(PluginConfig.colorize("&cPenggunaan: /bindwa unbind <player>"));
                    return true;
                }
                String targetName = args[1];
                Player targetPlayer = Bukkit.getPlayer(targetName);
                UUID targetUuid = targetPlayer != null ? targetPlayer.getUniqueId() : null;
                if (targetUuid == null) {
                    try {
                        targetUuid = UUID.fromString(targetName);
                    } catch (IllegalArgumentException e) {
                        sender.sendMessage(PluginConfig.colorize("&cPlayer atau UUID '" + targetName + "' tidak valid / tidak sedang online."));
                        return true;
                    }
                }

                UUID finalTargetUuid = targetUuid;
                Bukkit.getScheduler().runTaskAsynchronously(plugin, () -> {
                    boolean removed = bindingService.getDatabaseManager().unbind(finalTargetUuid);
                    bindingService.removePendingSession(finalTargetUuid);
                    Bukkit.getScheduler().runTask(plugin, () -> {
                        if (removed) {
                            sender.sendMessage(PluginConfig.colorize("&a[BindWA] Berhasil unbind data WhatsApp untuk &e" + targetName + "&a!"));
                            if (targetPlayer != null && targetPlayer.isOnline() && targetPlayer != sender) {
                                targetPlayer.sendMessage(PluginConfig.colorize("&e[BindWA] Nomor WhatsApp kamu telah di-unbind oleh admin."));
                            }
                        } else {
                            sender.sendMessage(PluginConfig.colorize("&e[BindWA] Akun player &f" + targetName + "&e tidak ditemukan di database."));
                        }
                    });
                });
                return true;
            }
        }

        if (!(sender instanceof Player player)) {
            sender.sendMessage(PluginConfig.colorize("&cHanya player yang dapat menggunakan command ini."));
            return true;
        }

        UUID uuid = player.getUniqueId();
        DatabaseManager db = bindingService.getDatabaseManager();
        PluginConfig config = bindingService.getConfig();

        // Cek status binding secara async agar tidak blocking main thread
        Bukkit.getScheduler().runTaskAsynchronously(plugin, () -> {
            Optional<WABinding> existing = db.getBinding(uuid);

            if (existing.isPresent() && existing.get().isVerified()) {
                // Player sudah bind & terverifikasi
                String maskedPhone = existing.get().getMaskedPhone();
                String msg = config.getMessage("already-bound", Map.of("phone", maskedPhone));
                Bukkit.getScheduler().runTask(plugin, () -> player.sendMessage(msg));
                return;
            }

            // Player belum bind -> deteksi platform dan buka UI yang sesuai di main thread
            boolean isBedrock = bindingService.isBedrockPlayer(uuid);
            Bukkit.getScheduler().runTask(plugin, () -> {
                if (!player.isOnline()) {
                    return;
                }
                if (isBedrock) {
                    bedrockFormFlow.openPhoneInputForm(player);
                } else {
                    javaAnvilFlow.openPhoneInputAnvil(player);
                }
            });
        });

        return true;
    }
}
