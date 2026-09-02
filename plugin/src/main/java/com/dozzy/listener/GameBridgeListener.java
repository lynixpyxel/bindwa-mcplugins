package com.dozzy.listener;

import com.dozzy.BindWAPlugin;
import com.dozzy.bridge.ChatBridgeManager;
import com.dozzy.config.PluginConfig;
import com.dozzy.database.DatabaseManager;
import org.bukkit.Bukkit;
import org.bukkit.ChatColor;
import org.bukkit.Location;
import org.bukkit.Material;
import org.bukkit.NamespacedKey;
import org.bukkit.World;
import org.bukkit.entity.EnderDragon;
import org.bukkit.entity.Item;
import org.bukkit.entity.ItemFrame;
import org.bukkit.entity.Player;
import org.bukkit.event.EventHandler;
import org.bukkit.event.EventPriority;
import org.bukkit.event.Listener;
import org.bukkit.event.entity.EntityDamageByEntityEvent;
import org.bukkit.event.entity.EntityDamageEvent;
import org.bukkit.event.entity.EntityDeathEvent;
import org.bukkit.event.entity.EntityPickupItemEvent;
import org.bukkit.event.entity.ItemDespawnEvent;
import org.bukkit.event.hanging.HangingBreakByEntityEvent;
import org.bukkit.event.player.AsyncPlayerChatEvent;
import org.bukkit.event.player.PlayerInteractEntityEvent;
import org.bukkit.event.player.PlayerInteractEvent;
import org.bukkit.event.player.PlayerItemBreakEvent;
import org.bukkit.inventory.ItemStack;
import org.bukkit.inventory.meta.ItemMeta;
import org.bukkit.persistence.PersistentDataContainer;
import org.bukkit.persistence.PersistentDataType;

import java.util.*;
import java.util.concurrent.ConcurrentHashMap;

public class GameBridgeListener implements Listener {

    private final BindWAPlugin plugin;
    private final ChatBridgeManager manager;
    private final PluginConfig config;

    private final NamespacedKey keyOwner;
    private final NamespacedKey keyOriginalOwner;
    private final NamespacedKey keyId;
    private final NamespacedKey keyNumber;

    private final Map<UUID, Long> frameDebounce = new ConcurrentHashMap<>();
    private final Map<UUID, Long> eggCooldowns = new ConcurrentHashMap<>();
    private final Set<UUID> destroyedElytraUids = ConcurrentHashMap.newKeySet();

    public GameBridgeListener(BindWAPlugin plugin, ChatBridgeManager manager) {
        this.plugin = plugin;
        this.manager = manager;
        this.config = plugin.getPluginConfig();

        this.keyOwner = new NamespacedKey(plugin, "elytra_owner");
        this.keyOriginalOwner = new NamespacedKey(plugin, "elytra_original_owner");
        this.keyId = new NamespacedKey(plugin, "elytra_uid");
        this.keyNumber = new NamespacedKey(plugin, "elytra_number");
    }

    @EventHandler(priority = EventPriority.LOW)
    public void onPlayerChat(AsyncPlayerChatEvent event) {
        String msg = event.getMessage().trim();
        String lower = msg.toLowerCase();

        if (lower.startsWith(".chat ") || lower.startsWith(".wa ")) {
            event.setCancelled(true);
            Player player = event.getPlayer();

            String chatContent;
            if (lower.startsWith(".chat ")) {
                chatContent = msg.substring(6).trim();
            } else {
                chatContent = msg.substring(4).trim();
            }

            if (chatContent.isEmpty()) {
                player.sendMessage(PluginConfig.colorize("&cPesan tidak boleh kosong."));
                return;
            }

            manager.sendChatMessage(player.getName(), chatContent);
            player.sendMessage(PluginConfig.colorize("&aChat berhasil dikirim ke grup WhatsApp!"));
        }
    }

    @EventHandler(priority = EventPriority.MONITOR, ignoreCancelled = true)
    public void onEntityDeath(EntityDeathEvent event) {
        if (!config.isNotificationEnderDragon()) {
            return;
        }

        if (event.getEntity() instanceof EnderDragon dragon) {
            Player killer = dragon.getKiller();
            String killerName;

            if (killer != null) {
                killerName = killer.getName();
            } else {
                killerName = dragon.getWorld().getPlayers().stream()
                        .min(Comparator.comparingDouble(p -> p.getLocation().distanceSquared(dragon.getLocation())))
                        .map(Player::getName)
                        .orElse("Player");
            }

            String message = "*Ender Dragon* telah berhasil dikalahkan oleh *" + killerName + "*!";
            manager.sendNotification("Ender Dragon Defeated", message);
            plugin.getLogger().info("[Game-Bridge] Notifikasi Ender Dragon dikirim: " + message);
        }
    }

    @EventHandler(priority = EventPriority.MONITOR, ignoreCancelled = true)
    public void onPlayerInteract(PlayerInteractEvent event) {
        if (!config.isNotificationDragonEgg()) {
            return;
        }

        Player player = event.getPlayer();
        if (player.getWorld().getEnvironment() == World.Environment.THE_END) {
            if (event.getClickedBlock() != null && event.getClickedBlock().getType() == Material.DRAGON_EGG) {
                handleDragonEggTaken(player);
            }
        }
    }

    // =========================================================================
    // ELYTRA MONOPOLY TRACKER — DETEKSI PENGAMBILAN DARI ITEM FRAME
    // =========================================================================

    /**
     * 1. Klik kanan ItemFrame
     */
    @EventHandler(priority = EventPriority.MONITOR, ignoreCancelled = true)
    public void onPlayerInteractEntity(PlayerInteractEntityEvent event) {
        if (!config.isNotificationElytra()) {
            return;
        }

        if (event.getRightClicked() instanceof ItemFrame frame) {
            ItemStack frameItem = frame.getItem();
            if (frameItem.getType() != Material.ELYTRA || isAlreadyTagged(frameItem)) {
                return;
            }

            Player player = event.getPlayer();
            UUID frameUuid = frame.getUniqueId();
            long now = System.currentTimeMillis();
            Long last = frameDebounce.get(frameUuid);
            if (last != null && now - last < 3000L) {
                return;
            }
            frameDebounce.put(frameUuid, now);

            Location loc = frame.getLocation();

            // Tag item langsung secara sinkron jika item masih di frame
            String uid = UUID.randomUUID().toString();
            tagElytra(frameItem, player, uid);

            // Validasi pada tick berikutnya
            Bukkit.getScheduler().runTaskLater(plugin, () -> {
                processFreshElytraPickup(player, loc, uid);
            }, 1L);
        }
    }

    /**
     * 2. Break / Attack ItemFrame oleh Player
     */
    @EventHandler(priority = EventPriority.MONITOR, ignoreCancelled = true)
    public void onHangingBreakByEntity(HangingBreakByEntityEvent event) {
        if (!config.isNotificationElytra()) {
            return;
        }

        if (event.getEntity() instanceof ItemFrame frame && event.getRemover() instanceof Player player) {
            ItemStack frameItem = frame.getItem();
            if (frameItem.getType() != Material.ELYTRA || isAlreadyTagged(frameItem)) {
                return;
            }

            UUID frameUuid = frame.getUniqueId();
            long now = System.currentTimeMillis();
            Long last = frameDebounce.get(frameUuid);
            if (last != null && now - last < 3000L) {
                return;
            }
            frameDebounce.put(frameUuid, now);

            Location loc = frame.getLocation();
            String uid = UUID.randomUUID().toString();
            tagElytra(frameItem, player, uid);

            Bukkit.getScheduler().runTaskLater(plugin, () -> {
                processFreshElytraPickup(player, loc, uid);
            }, 1L);
        }
    }

    @EventHandler(priority = EventPriority.MONITOR, ignoreCancelled = true)
    public void onEntityDamageByEntity(EntityDamageByEntityEvent event) {
        if (!config.isNotificationElytra()) {
            return;
        }

        if (event.getEntity() instanceof ItemFrame frame && event.getDamager() instanceof Player player) {
            if (config.isElytraBypassOp() && player.isOp()) {
                return;
            }

            ItemStack frameItem = frame.getItem();
            if (frameItem.getType() != Material.ELYTRA || isAlreadyTagged(frameItem)) {
                return;
            }

            UUID frameUuid = frame.getUniqueId();
            long now = System.currentTimeMillis();
            Long last = frameDebounce.get(frameUuid);
            if (last != null && now - last < 3000L) {
                return;
            }
            frameDebounce.put(frameUuid, now);

            Location loc = frame.getLocation();
            String uid = UUID.randomUUID().toString();
            tagElytra(frameItem, player, uid);

            Bukkit.getScheduler().runTaskLater(plugin, () -> {
                processFreshElytraPickup(player, loc, uid);
            }, 1L);
        }
    }

    /**
     * 3. Ambil Elytra dari Ground / Transfer Kepemilikan
     */
    @EventHandler(priority = EventPriority.MONITOR, ignoreCancelled = true)
    public void onItemPickup(EntityPickupItemEvent event) {
        if (!(event.getEntity() instanceof Player player)) {
            return;
        }

        ItemStack item = event.getItem().getItemStack();

        // Notifikasi Dragon Egg
        if (config.isNotificationDragonEgg() && item.getType() == Material.DRAGON_EGG && player.getWorld().getEnvironment() == World.Environment.THE_END) {
            handleDragonEggTaken(player);
        }

        // Notifikasi & Tracking Elytra
        if (config.isNotificationElytra() && item.getType() == Material.ELYTRA) {
            if (isAlreadyTagged(item)) {
                // Item sudah pernah di-tag -> Cek apakah pemiliknya berubah (Transfer kepemilikan)
                String currentOwnerUuidStr = getElytraOwnerUuid(item);
                if (currentOwnerUuidStr != null && !currentOwnerUuidStr.isEmpty()) {
                    try {
                        UUID currentOwnerUuid = UUID.fromString(currentOwnerUuidStr);
                        if (!currentOwnerUuid.equals(player.getUniqueId())) {
                            // Player B mengambil Elytra milik Player A (Transfer)
                            handleElytraTransfer(item, currentOwnerUuid, player);
                        }
                    } catch (IllegalArgumentException ignored) {}
                }
                // Jika diambil oleh pemilik yang sama (drop lalu ambil lagi) -> SKIP, tidak ada notif double
                return;
            }

            // Jika item belum pernah di-tag (fresh pickup)
            if (config.isElytraBypassOp() && player.isOp()) {
                return;
            }

            String uid = UUID.randomUUID().toString();
            tagElytra(item, player, uid);
            processFreshElytraPickup(player, event.getItem().getLocation(), uid);
        }
    }

    /**
     * 4. Elytra Rusak / Hancur saat Dipakai (Durability 0)
     */
    @EventHandler(priority = EventPriority.MONITOR, ignoreCancelled = true)
    public void onItemBreak(PlayerItemBreakEvent event) {
        ItemStack broken = event.getBrokenItem();
        if (broken.getType() == Material.ELYTRA && isAlreadyTagged(broken)) {
            Player player = event.getPlayer();
            handleElytraDestroyed(broken, player.getUniqueId(), player.getName(), "rusak/hancur saat dipakai");
        }
    }

    /**
     * 5. Elytra Terbakar Lava / Void / Meledak
     */
    @EventHandler(priority = EventPriority.MONITOR, ignoreCancelled = true)
    public void onItemDamage(EntityDamageEvent event) {
        if (event.getEntity() instanceof Item itemEntity) {
            ItemStack stack = itemEntity.getItemStack();
            if (stack.getType() == Material.ELYTRA && isAlreadyTagged(stack)) {
                if (itemEntity.getHealth() - event.getFinalDamage() <= 0) {
                    String ownerUuidStr = getElytraOwnerUuid(stack);
                    if (ownerUuidStr != null) {
                        try {
                            UUID ownerUuid = UUID.fromString(ownerUuidStr);
                            handleElytraDestroyed(stack, ownerUuid, null, "terbakar/hancur");
                        } catch (IllegalArgumentException ignored) {}
                    }
                }
            }
        }
    }

    /**
     * 6. Elytra Despawn di Tanah
     */
    @EventHandler(priority = EventPriority.MONITOR, ignoreCancelled = true)
    public void onItemDespawn(ItemDespawnEvent event) {
        ItemStack stack = event.getEntity().getItemStack();
        if (stack.getType() == Material.ELYTRA && isAlreadyTagged(stack)) {
            String ownerUuidStr = getElytraOwnerUuid(stack);
            if (ownerUuidStr != null) {
                try {
                    UUID ownerUuid = UUID.fromString(ownerUuidStr);
                    handleElytraDestroyed(stack, ownerUuid, null, "despawn di tanah");
                } catch (IllegalArgumentException ignored) {}
            }
        }
    }

    // =========================================================================
    // HELPER METHODS
    // =========================================================================

    private boolean isAlreadyTagged(ItemStack item) {
        if (item == null || item.getType() != Material.ELYTRA || !item.hasItemMeta()) {
            return false;
        }
        ItemMeta meta = item.getItemMeta();
        if (meta == null) return false;
        return meta.getPersistentDataContainer().has(keyId, PersistentDataType.STRING);
    }

    private String getElytraOwnerUuid(ItemStack item) {
        if (item == null || !item.hasItemMeta()) return null;
        ItemMeta meta = item.getItemMeta();
        if (meta == null) return null;
        return meta.getPersistentDataContainer().get(keyOwner, PersistentDataType.STRING);
    }

    private void tagElytra(ItemStack elytra, Player player, String uid) {
        if (elytra == null || elytra.getType() != Material.ELYTRA) return;
        ItemMeta meta = elytra.getItemMeta();
        if (meta == null) return;

        PersistentDataContainer pdc = meta.getPersistentDataContainer();
        pdc.set(keyOwner, PersistentDataType.STRING, player.getUniqueId().toString());
        pdc.set(keyOriginalOwner, PersistentDataType.STRING, player.getName());
        pdc.set(keyId, PersistentDataType.STRING, uid);

        List<String> lore = new ArrayList<>();
        lore.add(ChatColor.GRAY + "Diambil oleh: " + ChatColor.YELLOW + player.getName());
        meta.setLore(lore);

        elytra.setItemMeta(meta);
    }

    private void updateElytraHolderLore(ItemStack elytra, String originalOwner, String currentHolder) {
        if (elytra == null || !elytra.hasItemMeta()) return;
        ItemMeta meta = elytra.getItemMeta();
        if (meta == null) return;

        List<String> lore = new ArrayList<>();
        lore.add(ChatColor.GRAY + "Diambil oleh: " + ChatColor.YELLOW + originalOwner);
        lore.add(ChatColor.GRAY + "Dipegang oleh: " + ChatColor.GREEN + currentHolder);
        meta.setLore(lore);

        elytra.setItemMeta(meta);
    }

    private void processFreshElytraPickup(Player player, Location loc, String itemUid) {
        if (config.isElytraBypassOp() && player.isOp()) {
            plugin.getLogger().info("[ElytraTracker] Pengambilan elytra oleh OP " + player.getName() + " diabaikan (bypass-op: true).");
            return;
        }

        UUID ownerUuid = player.getUniqueId();
        String ownerName = player.getName();
        String worldName = loc.getWorld() != null ? loc.getWorld().getName() : "world";
        int x = loc.getBlockX();
        int y = loc.getBlockY();
        int z = loc.getBlockZ();

        // Pastikan item di inventory pemain juga sudah ter-tag
        for (ItemStack invItem : player.getInventory().getContents()) {
            if (invItem != null && invItem.getType() == Material.ELYTRA && !isAlreadyTagged(invItem)) {
                tagElytra(invItem, player, itemUid);
                break;
            }
        }

        Bukkit.getScheduler().runTaskAsynchronously(plugin, () -> {
            // 1. Increment total count di database
            int newCount = plugin.getDatabaseManager().incrementAndGetElytraCount(ownerUuid, ownerName);

            // 2. Log pickup audit trail
            plugin.getDatabaseManager().logElytraPickup(UUID.fromString(itemUid), ownerUuid, ownerName, newCount, worldName, x, y, z);

            // Update nomor urut di lore item
            Bukkit.getScheduler().runTask(plugin, () -> {
                for (ItemStack invItem : player.getInventory().getContents()) {
                    if (invItem != null && invItem.getType() == Material.ELYTRA && isAlreadyTagged(invItem)) {
                        ItemMeta m = invItem.getItemMeta();
                        if (m != null) {
                            String uidInPdc = m.getPersistentDataContainer().get(keyId, PersistentDataType.STRING);
                            if (itemUid.equals(uidInPdc)) {
                                m.getPersistentDataContainer().set(keyNumber, PersistentDataType.INTEGER, newCount);
                                List<String> lore = new ArrayList<>();
                                lore.add(ChatColor.GRAY + "Diambil oleh: " + ChatColor.YELLOW + ownerName);
                                lore.add(ChatColor.GRAY + "Elytra ke-" + newCount);
                                m.setLore(lore);
                                invItem.setItemMeta(m);
                                break;
                            }
                        }
                    }
                }
            });

            // 3. Kirim Notifikasi Real-Time ke WhatsApp
            String realTimeMsg = String.format("*%s* mendapatkan elytra ke-*%d*!\nLokasi: *%s* (%d, %d, %d)",
                    ownerName, newCount, worldName, x, y, z);
            manager.sendNotification("Elytra Obtained", realTimeMsg);

            // 4. Kirim Update Leaderboard ke WhatsApp
            broadcastLeaderboard();

            plugin.getLogger().info("[ElytraTracker] " + ownerName + " mendapatkan elytra ke-" + newCount + " di " + worldName + " (" + x + ", " + y + ", " + z + ")");
        });
    }

    private void handleElytraTransfer(ItemStack elytra, UUID oldOwnerUuid, Player newHolder) {
        ItemMeta meta = elytra.getItemMeta();
        if (meta == null) return;

        String origOwner = meta.getPersistentDataContainer().get(keyOriginalOwner, PersistentDataType.STRING);
        if (origOwner == null || origOwner.isEmpty()) {
            origOwner = plugin.getDatabaseManager().getPlayerNameByUuid(oldOwnerUuid);
        }

        // Update tag PDC ke holder baru
        meta.getPersistentDataContainer().set(keyOwner, PersistentDataType.STRING, newHolder.getUniqueId().toString());
        elytra.setItemMeta(meta);
        updateElytraHolderLore(elytra, origOwner, newHolder.getName());

        boolean isNewHolderOp = config.isElytraBypassOp() && newHolder.isOp();

        String finalOrigOwner = origOwner;
        Bukkit.getScheduler().runTaskAsynchronously(plugin, () -> {
            String oldOwnerName = plugin.getDatabaseManager().getPlayerNameByUuid(oldOwnerUuid);

            // Kurangi count dari pemilik lama
            plugin.getDatabaseManager().decrementElytraCount(oldOwnerUuid);

            // Tambah count untuk pemilik baru jika bukan OP dengan bypass
            if (!isNewHolderOp) {
                plugin.getDatabaseManager().incrementAndGetElytraCount(newHolder.getUniqueId(), newHolder.getName());
            }

            // Kirim notifikasi transfer ke WA jika bukan OP bypass
            if (!isNewHolderOp) {
                String transferMsg = String.format("🔄 Elytra milik *%s* telah berpindah tangan ke *%s*!", oldOwnerName, newHolder.getName());
                manager.sendNotification("Elytra Transferred", transferMsg);
                broadcastLeaderboard();
            }

            plugin.getLogger().info("[ElytraTracker] Transfer elytra dari " + oldOwnerName + " ke " + newHolder.getName());
        });
    }

    private void handleElytraDestroyed(ItemStack elytra, UUID ownerUuid, String ownerName, String reason) {
        ItemMeta meta = elytra.getItemMeta();
        String uid = meta != null ? meta.getPersistentDataContainer().get(keyId, PersistentDataType.STRING) : null;
        if (uid != null) {
            try {
                UUID itemUuid = UUID.fromString(uid);
                if (!destroyedElytraUids.add(itemUuid)) {
                    return; // Sudah pernah diproses agar tidak double decrement
                }
            } catch (IllegalArgumentException ignored) {}
        }

        Bukkit.getScheduler().runTaskAsynchronously(plugin, () -> {
            String targetName = ownerName != null ? ownerName : plugin.getDatabaseManager().getPlayerNameByUuid(ownerUuid);

            // Kurangi counter player
            plugin.getDatabaseManager().decrementElytraCount(ownerUuid);

            // Kirim notifikasi kerusakan ke WA
            String destroyedMsg = String.format("Elytra milik *%s* telah %s!", targetName, reason);
            manager.sendNotification("Elytra Destroyed", destroyedMsg);

            // Update Leaderboard
            broadcastLeaderboard();

            plugin.getLogger().info("[ElytraTracker] Elytra milik " + targetName + " " + reason + " (Count dikurangi)");
        });
    }

    private void broadcastLeaderboard() {
        List<DatabaseManager.ElytraLeaderboardEntry> leaderboard = plugin.getDatabaseManager().getElytraLeaderboard(10);
        if (!leaderboard.isEmpty()) {
            StringBuilder sb = new StringBuilder("*Leaderboard Elytra*\n");
            int rank = 1;
            for (DatabaseManager.ElytraLeaderboardEntry entry : leaderboard) {
                sb.append(rank++).append(". ").append(entry.name())
                  .append(" — ").append(entry.count()).append(" elytra\n");
            }
            manager.sendNotification("Elytra Leaderboard", sb.toString().trim());
        }
    }

    private void handleDragonEggTaken(Player player) {
        UUID uuid = player.getUniqueId();
        long now = System.currentTimeMillis();
        Long last = eggCooldowns.get(uuid);

        // Cooldown 2 menit per player
        if (last == null || now - last > 120_000L) {
            eggCooldowns.put(uuid, now);
            String message = "*Dragon Egg* telah diambil/disentuh oleh *" + player.getName() + "*!";
            manager.sendNotification("Dragon Egg Taken", message);
            plugin.getLogger().info("[Game-Bridge] Notifikasi Dragon Egg dikirim untuk " + player.getName());
        }
    }
}
