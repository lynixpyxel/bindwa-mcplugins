package com.dozzy.listener;

import com.dozzy.BindWAPlugin;
import com.dozzy.bridge.ChatBridgeManager;
import com.dozzy.config.PluginConfig;
import org.bukkit.Material;
import org.bukkit.World;
import org.bukkit.entity.EnderDragon;
import org.bukkit.entity.Player;
import org.bukkit.event.EventHandler;
import org.bukkit.event.EventPriority;
import org.bukkit.event.Listener;
import org.bukkit.event.entity.EntityDeathEvent;
import org.bukkit.event.entity.EntityPickupItemEvent;
import org.bukkit.event.player.AsyncPlayerChatEvent;
import org.bukkit.event.player.PlayerInteractEvent;
import org.bukkit.inventory.ItemStack;

import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;

public class GameBridgeListener implements Listener {

    private final BindWAPlugin plugin;
    private final ChatBridgeManager manager;
    private final PluginConfig config;

    private final Set<String> loggedElytras = ConcurrentHashMap.newKeySet();

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
            player.sendMessage(PluginConfig.colorize("&aChat dikirim ke grup WhatsApp!"));
        }
    }

    @EventHandler
    public void onEntityDeath(EntityDeathEvent event) {
        if (!config.isNotificationEnderDragon()) {
            return;
        }

        if (event.getEntity() instanceof EnderDragon) {
            Player killer = event.getEntity().getKiller();
            String message;
            if (killer != null) {
                message = "Ender Dragon telah dibunuh oleh *" + killer.getName() + "*!";
            } else {
                message = "Ender Dragon berhasil dikalahkan!";
            }

            manager.sendNotification("Ender Dragon Death", message);
            plugin.getLogger().info("[Game-Bridge] Notifikasi Ender Dragon dikirim: " + message);
        }
    }

    @EventHandler
    public void onPlayerInteract(PlayerInteractEvent event) {
        if (!config.isNotificationDragonEgg()) {
            return;
        }

        Player player = event.getPlayer();
        if (player.getWorld().getEnvironment() == World.Environment.THE_END) {
            if (event.getClickedBlock() != null && event.getClickedBlock().getType() == Material.DRAGON_EGG) {
                String message = "Dragon Egg disentuh/diambil oleh *" + player.getName() + "*!";
                manager.sendNotification("Dragon Egg Taken", message);
                plugin.getLogger().info("[Game-Bridge] Notifikasi Dragon Egg dikirim untuk " + player.getName());
            }
        }
    }

    @EventHandler
    public void onItemPickup(EntityPickupItemEvent event) {
        if (!(event.getEntity() instanceof Player player)) {
            return;
        }

        ItemStack item = event.getItem().getItemStack();

        // Notifikasi Dragon Egg jika diambil dari ground
        if (config.isNotificationDragonEgg() && item.getType() == Material.DRAGON_EGG && player.getWorld().getEnvironment() == World.Environment.THE_END) {
            String message = "Dragon Egg diambil oleh *" + player.getName() + "*!";
            manager.sendNotification("Dragon Egg Taken", message);
            plugin.getLogger().info("[Game-Bridge] Notifikasi Dragon Egg Pickup dikirim untuk " + player.getName());
        }

        // Notifikasi Elytra jika diambil di The End
        if (config.isNotificationElytra() && item.getType() == Material.ELYTRA && player.getWorld().getEnvironment() == World.Environment.THE_END) {
            String itemKey = player.getUniqueId().toString() + "_" + System.currentTimeMillis() / 10000L;
            if (loggedElytras.add(itemKey)) {
                String message = "*" + player.getName() + "* mendapatkan *Elytra*!";
                manager.sendNotification("Elytra Obtained", message);
                plugin.getLogger().info("[Game-Bridge] Notifikasi Elytra dikirim untuk " + player.getName());
            }
        }
    }
}
