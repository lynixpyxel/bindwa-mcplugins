package com.dozzy.listener;

import com.dozzy.BindWAPlugin;
import com.dozzy.bridge.ChatBridgeManager;
import com.dozzy.config.PluginConfig;
import org.bukkit.Material;
import org.bukkit.World;
import org.bukkit.entity.EnderDragon;
import org.bukkit.entity.ItemFrame;
import org.bukkit.entity.Player;
import org.bukkit.event.EventHandler;
import org.bukkit.event.EventPriority;
import org.bukkit.event.Listener;
import org.bukkit.event.entity.EntityDeathEvent;
import org.bukkit.event.entity.EntityPickupItemEvent;
import org.bukkit.event.player.AsyncPlayerChatEvent;
import org.bukkit.event.player.PlayerAdvancementDoneEvent;
import org.bukkit.event.player.PlayerInteractEntityEvent;
import org.bukkit.event.player.PlayerInteractEvent;
import org.bukkit.inventory.ItemStack;

import java.util.Comparator;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;

public class GameBridgeListener implements Listener {

    private final BindWAPlugin plugin;
    private final ChatBridgeManager manager;
    private final PluginConfig config;

    private final Map<UUID, Long> elytraCooldowns = new ConcurrentHashMap<>();
    private final Map<UUID, Long> eggCooldowns = new ConcurrentHashMap<>();

    public GameBridgeListener(BindWAPlugin plugin, ChatBridgeManager manager) {
        this.plugin = plugin;
        this.manager = manager;
        this.config = plugin.getPluginConfig();
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
                // Cari player terdekat di The End jika killer tidak langsung terdeteksi
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
    public void onAdvancementDone(PlayerAdvancementDoneEvent event) {
        if (!config.isNotificationElytra()) {
            return;
        }

        String advKey = event.getAdvancement().getKey().toString();
        // Advancement perolehan Elytra: minecraft:end/elytra
        if ("minecraft:end/elytra".equalsIgnoreCase(advKey)) {
            Player player = event.getPlayer();
            handleElytraObtained(player);
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

    @EventHandler(priority = EventPriority.MONITOR, ignoreCancelled = true)
    public void onPlayerInteractEntity(PlayerInteractEntityEvent event) {
        if (!config.isNotificationElytra()) {
            return;
        }

        Player player = event.getPlayer();
        if (player.getWorld().getEnvironment() == World.Environment.THE_END) {
            if (event.getRightClicked() instanceof ItemFrame itemFrame) {
                ItemStack item = itemFrame.getItem();
                if (item.getType() == Material.ELYTRA) {
                    handleElytraObtained(player);
                }
            }
        }
    }

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

        // Notifikasi Elytra
        if (config.isNotificationElytra() && item.getType() == Material.ELYTRA && player.getWorld().getEnvironment() == World.Environment.THE_END) {
            handleElytraObtained(player);
        }
    }

    private void handleElytraObtained(Player player) {
        UUID uuid = player.getUniqueId();
        long now = System.currentTimeMillis();
        Long last = elytraCooldowns.get(uuid);

        // Cooldown 5 menit per player agar tidak spam jika elytra dilepas-pasang
        if (last == null || now - last > 300_000L) {
            elytraCooldowns.put(uuid, now);
            String message = "*" + player.getName() + "* berhasil mendapatkan *Elytra* di The End!";
            manager.sendNotification("Elytra Obtained", message);
            plugin.getLogger().info("[Game-Bridge] Notifikasi Elytra dikirim untuk " + player.getName());
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
