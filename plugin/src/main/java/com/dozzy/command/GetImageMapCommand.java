package com.dozzy.command;

import com.dozzy.BindWAPlugin;
import com.dozzy.config.PluginConfig;
import com.dozzy.database.ImageMapClaim;
import org.bukkit.Bukkit;
import org.bukkit.Sound;
import org.bukkit.command.Command;
import org.bukkit.command.CommandExecutor;
import org.bukkit.command.CommandSender;
import org.bukkit.command.TabCompleter;
import org.bukkit.entity.Player;
import org.bukkit.util.StringUtil;
import org.jetbrains.annotations.NotNull;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.stream.Collectors;

public class GetImageMapCommand implements CommandExecutor, TabCompleter {

    private final BindWAPlugin plugin;

    public GetImageMapCommand(BindWAPlugin plugin) {
        this.plugin = plugin;
    }

    @Override
    public boolean onCommand(@NotNull CommandSender sender, @NotNull Command command, @NotNull String label, @NotNull String[] args) {
        if (!(sender instanceof Player)) {
            sender.sendMessage(PluginConfig.colorize("&cPerintah ini hanya dapat dijalankan oleh pemain di dalam game."));
            return true;
        }

        Player player = (Player) sender;

        if (!player.hasPermission("bindwa.imagemap.claim")) {
            player.sendMessage(PluginConfig.colorize("&cKamu tidak memiliki izin untuk mengklaim imagemap."));
            return true;
        }

        Bukkit.getScheduler().runTaskAsynchronously(plugin, () -> {
            List<ImageMapClaim> unclaimedList = plugin.getDatabaseManager().getUnclaimedImageMaps(player.getName());
            List<ImageMapClaim> allList = plugin.getDatabaseManager().getAllClaimsByPlayer(player.getName());

            if (unclaimedList.isEmpty() && allList.isEmpty()) {
                Bukkit.getScheduler().runTask(plugin, () -> {
                    player.sendMessage(PluginConfig.colorize("&cKamu tidak memiliki antrean pesanan imagemap."));
                    player.sendMessage(PluginConfig.colorize("&7Pesan poster peta melalui bot WhatsApp dengan mengetik .imagemap!"));
                });
                return;
            }

            ImageMapClaim targetClaim = null;
            boolean isRetry = false;

            if (args.length > 0) {
                String arg0 = args[0].toLowerCase();
                if (arg0.equals("retry") && args.length > 1) {
                    arg0 = args[1].toLowerCase();
                    isRetry = true;
                } else if (arg0.equals("retry")) {
                    isRetry = true;
                }

                if (!isRetry) {
                    for (ImageMapClaim c : unclaimedList) {
                        if (c.getMapName().equalsIgnoreCase(arg0) || c.getOrderId().equalsIgnoreCase(arg0)) {
                            targetClaim = c;
                            break;
                        }
                    }
                }

                // Jika tidak ditemukan di antrean unclaimed, cari di seluruh riwayat klaim untuk retry
                if (targetClaim == null) {
                    for (ImageMapClaim c : allList) {
                        if (isRetry || c.getMapName().equalsIgnoreCase(arg0) || c.getOrderId().equalsIgnoreCase(arg0)) {
                            targetClaim = c;
                            break;
                        }
                    }
                }

                if (targetClaim == null) {
                    final String query = arg0;
                    Bukkit.getScheduler().runTask(plugin, () -> {
                        player.sendMessage(PluginConfig.colorize("&cTidak ditemukan pesanan imagemap dengan nama/ID '&e" + query + "&c'."));
                        if (!unclaimedList.isEmpty()) {
                            player.sendMessage(PluginConfig.colorize("&7Daftar map yang belum kamu klaim:"));
                            for (ImageMapClaim c : unclaimedList) {
                                player.sendMessage(PluginConfig.colorize("&8- &e" + c.getMapName() + " &7(ID: " + c.getOrderId() + ")"));
                            }
                        } else {
                            player.sendMessage(PluginConfig.colorize("&7Riwayat map yang pernah kamu pesan:"));
                            for (ImageMapClaim c : allList) {
                                player.sendMessage(PluginConfig.colorize("&8- &e" + c.getMapName() + " &7(ID: " + c.getOrderId() + ") - Ketik &b/getimagemap retry " + c.getMapName()));
                            }
                        }
                    });
                    return;
                }
            } else {
                if (!unclaimedList.isEmpty()) {
                    targetClaim = unclaimedList.get(0);
                } else {
                    // Jika unclaimed kosong tetapi ada riwayat, klaim yang paling baru
                    targetClaim = allList.get(0);
                }
            }

            final ImageMapClaim claimToGive = targetClaim;
            final int remainingCount = Math.max(0, unclaimedList.size() - 1);

            Bukkit.getScheduler().runTask(plugin, () -> {
                if (!player.isOnline()) {
                    return;
                }

                if (player.getInventory().firstEmpty() == -1) {
                    player.sendMessage(PluginConfig.colorize("&cInventory kamu penuh! Kosongkan minimal 1 slot inventory terlebih dahulu sebelum mengklaim imagemap."));
                    return;
                }

                Runnable giveTask = () -> {
                    if (!player.isOnline()) {
                        return;
                    }

                    // Jalankan perintah give imagemap dari console dengan format {player}:{name}
                    String giveCmd = plugin.getPluginConfig().buildImageMapGiveCommand(player.getName(), claimToGive.getMapName());
                    plugin.getLogger().info("[ImageMap] Memberikan map ke " + player.getName() + " dengan perintah: " + giveCmd);
                    Bukkit.dispatchCommand(Bukkit.getConsoleSender(), giveCmd);

                    // Tandai sebagai sudah diklaim di database SQLite
                    Bukkit.getScheduler().runTaskAsynchronously(plugin, () -> {
                        plugin.getDatabaseManager().markImageMapClaimed(claimToGive.getId());
                    });

                    player.sendMessage(PluginConfig.colorize("&a[ImageMap] Berhasil mengklaim poster '&e" + claimToGive.getMapName() + "&a' ke inventory!"));
                    if (remainingCount > 0) {
                        player.sendMessage(PluginConfig.colorize("&7Masih ada &e" + remainingCount + " &7imagemap lain yang belum diklaim. Ketik &b/getimagemap &7lagi untuk mengambil berikutnya."));
                    }
                    player.playSound(player.getLocation(), Sound.ENTITY_PLAYER_LEVELUP, 1.0f, 1.0f);
                };

                // Pastikan pembuatan map di ImageFrame sudah ada jika image_url tersedia
                if (claimToGive.getImageUrl() != null && !claimToGive.getImageUrl().isEmpty()) {
                    String createCmd = plugin.getPluginConfig().buildImageMapCreateCommand(
                            player.getName(),
                            claimToGive.getMapName(),
                            claimToGive.getImageUrl(),
                            claimToGive.getWidth(),
                            claimToGive.getHeight()
                    );
                    plugin.getLogger().info("[ImageMap] Memastikan pembuatan map di ImageFrame: " + createCmd);
                    Bukkit.dispatchCommand(Bukkit.getConsoleSender(), createCmd);

                    // Beri jeda 15 tick (~750ms) agar ImageFrame selesai mendownload dan merender gambar sebelum di-give
                    Bukkit.getScheduler().runTaskLater(plugin, giveTask, 15L);
                } else {
                    giveTask.run();
                }
            });
        });

        return true;
    }

    @Override
    public List<String> onTabComplete(@NotNull CommandSender sender, @NotNull Command command, @NotNull String alias, @NotNull String[] args) {
        if (args.length == 1 && sender instanceof Player) {
            Player player = (Player) sender;
            List<ImageMapClaim> claims = plugin.getDatabaseManager().getUnclaimedImageMaps(player.getName());
            List<String> suggestions = claims.stream().map(ImageMapClaim::getMapName).collect(Collectors.toList());
            if (suggestions.isEmpty()) {
                List<ImageMapClaim> all = plugin.getDatabaseManager().getAllClaimsByPlayer(player.getName());
                suggestions.addAll(all.stream().map(ImageMapClaim::getMapName).collect(Collectors.toList()));
                suggestions.add("retry");
            }
            return StringUtil.copyPartialMatches(args[0], suggestions, new ArrayList<>());
        } else if (args.length == 2 && args[0].equalsIgnoreCase("retry") && sender instanceof Player) {
            Player player = (Player) sender;
            List<ImageMapClaim> all = plugin.getDatabaseManager().getAllClaimsByPlayer(player.getName());
            List<String> names = all.stream().map(ImageMapClaim::getMapName).collect(Collectors.toList());
            return StringUtil.copyPartialMatches(args[1], names, new ArrayList<>());
        }
        return Collections.emptyList();
    }
}
