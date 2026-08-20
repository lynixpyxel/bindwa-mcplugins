package com.dozzy.command;

import com.dozzy.BindWAPlugin;
import com.dozzy.config.PluginConfig;
import com.dozzy.database.DatabaseManager;
import org.bukkit.Bukkit;
import org.bukkit.command.Command;
import org.bukkit.command.CommandExecutor;
import org.bukkit.command.CommandSender;
import org.bukkit.entity.Player;
import org.jetbrains.annotations.NotNull;

import java.util.List;

public class ElytraCommand implements CommandExecutor {

    private final BindWAPlugin plugin;
    private final DatabaseManager databaseManager;

    public ElytraCommand(BindWAPlugin plugin, DatabaseManager databaseManager) {
        this.plugin = plugin;
        this.databaseManager = databaseManager;
    }

    private boolean isOpOrAdmin(CommandSender sender) {
        return sender.isOp() || sender.hasPermission("elytratracker.admin") || sender.hasPermission("bindwa.admin");
    }

    @Override
    public boolean onCommand(@NotNull CommandSender sender, @NotNull Command command, @NotNull String label, @NotNull String[] args) {
        String cmdName = command.getName().toLowerCase();

        if (cmdName.equals("elytracheck")) {
            if (!isOpOrAdmin(sender)) {
                sender.sendMessage(PluginConfig.colorize("&cKamu tidak memiliki izin untuk menggunakan perintah ini (Hanya OP)."));
                return true;
            }

            if (args.length < 1) {
                sender.sendMessage(PluginConfig.colorize("&cPenggunaan: /elytracheck <player>"));
                return true;
            }

            String targetName = args[0];
            Player target = Bukkit.getPlayer(targetName);

            Bukkit.getScheduler().runTaskAsynchronously(plugin, () -> {
                int count = 0;
                if (target != null) {
                    count = databaseManager.getPlayerElytraCount(target.getUniqueId());
                } else {
                    var offline = Bukkit.getOfflinePlayer(targetName);
                    if (offline != null && offline.getUniqueId() != null) {
                        count = databaseManager.getPlayerElytraCount(offline.getUniqueId());
                    }
                }

                int finalCount = count;
                Bukkit.getScheduler().runTask(plugin, () -> {
                    sender.sendMessage(PluginConfig.colorize("&a[ElytraTracker] Total elytra yang dipegang oleh &e" + targetName + "&a: &f" + finalCount + " elytra"));
                });
            });

            return true;
        }

        // /elytraboard
        if (args.length > 0) {
            // Subcommand: /elytraboard set bypassop <true/false>
            if (args[0].equalsIgnoreCase("set")) {
                if (!isOpOrAdmin(sender)) {
                    sender.sendMessage(PluginConfig.colorize("&cYou do not have permission to change the elytra configuration (Only OP)."));
                    return true;
                }

                if (args.length >= 3 && args[1].equalsIgnoreCase("bypassop")) {
                    String valStr = args[2].toLowerCase();
                    boolean bypass = valStr.equals("true") || valStr.equals("on") || valStr.equals("1") || valStr.equals("yes");
                    plugin.getPluginConfig().setElytraBypassOp(bypass);
                    plugin.getConfig().set("chat-bridge.notifications.bypass-op", bypass);
                    plugin.saveConfig();

                    String statusText = bypass ? "&aBYPASS" : "&eBYPASS NOT ACTIVE";
                    sender.sendMessage(PluginConfig.colorize("&a[ElytraTracker] &fBypass OP &a: " + statusText));
                    return true;
                }

                sender.sendMessage(PluginConfig.colorize("&cUsage: /elytraboard set bypassop <true/false>"));
                return true;
            }

            if (args[0].equalsIgnoreCase("reset") || args[0].equalsIgnoreCase("clear")) {
                if (!isOpOrAdmin(sender)) {
                    sender.sendMessage(PluginConfig.colorize("&cYou do not have permission to reset the Elytra leaderboard"));
                    return true;
                }

                if (args.length >= 2 && !args[1].equalsIgnoreCase("all")) {
                    // Reset single player: /elytraboard reset <player>
                    String targetName = args[1];
                    Player target = Bukkit.getPlayer(targetName);
                    var offline = (target == null) ? Bukkit.getOfflinePlayer(targetName) : null;
                    var targetUuid = (target != null) ? target.getUniqueId() : (offline != null ? offline.getUniqueId() : null);

                    if (targetUuid == null) {
                        sender.sendMessage(PluginConfig.colorize("&cPlayer '" + targetName + "' tidak ditemukan."));
                        return true;
                    }

                    Bukkit.getScheduler().runTaskAsynchronously(plugin, () -> {
                        databaseManager.resetPlayerElytra(targetUuid);
                        Bukkit.getScheduler().runTask(plugin, () -> {
                            sender.sendMessage(PluginConfig.colorize("&a[ElytraTracker] Elytra data for &e" + targetName + "&a reset to 0 by an OP."));
                        });
                    });
                    return true;
                }

                // Reset ALL: /elytraboard reset / /elytraboard reset all
                Bukkit.getScheduler().runTaskAsynchronously(plugin, () -> {
                    databaseManager.resetAllElytraLeaderboard();
                    plugin.getChatBridgeManager().sendNotification("Elytra Leaderboard Reset", "*Leaderboard has been reset by an OP.*");

                    Bukkit.getScheduler().runTask(plugin, () -> {
                        sender.sendMessage(PluginConfig.colorize("&a[ElytraTracker] Elytra leaderboard database has been reset by an OP!"));
                    });
                });
                return true;
            }
        }

        // Default: Tampilkan leaderboard
        Bukkit.getScheduler().runTaskAsynchronously(plugin, () -> {
            List<DatabaseManager.ElytraLeaderboardEntry> leaderboard = databaseManager.getElytraLeaderboard(10);
            boolean isBypass = plugin.getPluginConfig().isElytraBypassOp();

            Bukkit.getScheduler().runTask(plugin, () -> {
                sender.sendMessage(PluginConfig.colorize("&6&lELYTRA'S LEADERBOARD"));
                sender.sendMessage(PluginConfig.colorize("&8━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"));

                if (leaderboard.isEmpty()) {
                    sender.sendMessage(PluginConfig.colorize("&7Belum ada data elytra yang tercatat."));
                } else {
                    int rank = 1;
                    for (DatabaseManager.ElytraLeaderboardEntry entry : leaderboard) {
                        sender.sendMessage(PluginConfig.colorize("&e" + rank + ". &f" + entry.name() + " &7— &a" + entry.count() + " elytra"));
                        rank++;
                    }
                }

                sender.sendMessage(PluginConfig.colorize("&8━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"));
                if (isOpOrAdmin(sender)) {
                    sender.sendMessage(PluginConfig.colorize("&7&oStatus bypass OP: " + (isBypass ? "&aTrue (Bypass)" : "&eFalse (Counted)")));
                    sender.sendMessage(PluginConfig.colorize("&7&oPerintah OP: '/elytraboard set bypassop <true/false>' | '/elytraboard reset'"));
                }
            });
        });

        return true;
    }
}
