package com.dozzy.listener;

import com.dozzy.BindWAPlugin;
import com.dozzy.config.PluginConfig;
import com.dozzy.database.DatabaseManager;
import com.dozzy.http.BotApiClient;
import com.dozzy.http.SendOtpResult;
import com.dozzy.service.BindingService;
import org.bukkit.Bukkit;
import org.bukkit.Material;
import org.bukkit.entity.Player;
import org.bukkit.event.EventHandler;
import org.bukkit.event.EventPriority;
import org.bukkit.event.Listener;
import org.bukkit.event.inventory.InventoryClickEvent;
import org.bukkit.event.inventory.InventoryType;
import org.bukkit.event.inventory.PrepareAnvilEvent;
import org.bukkit.inventory.AnvilInventory;
import org.bukkit.inventory.ItemStack;
import org.bukkit.inventory.meta.ItemMeta;

import java.util.List;
import java.util.Map;
import java.util.UUID;

public class JavaAnvilListener implements Listener {

    public static final String ANVIL_TITLE = PluginConfig.colorize("&8Masukkan Nomor WA");

    private final BindWAPlugin plugin;
    private final BindingService bindingService;
    private final PluginConfig config;
    private final DatabaseManager databaseManager;
    private final BotApiClient apiClient;
    private JavaChatListener chatListener;

    public JavaAnvilListener(BindWAPlugin plugin, BindingService bindingService) {
        this.plugin = plugin;
        this.bindingService = bindingService;
        this.config = bindingService.getConfig();
        this.databaseManager = bindingService.getDatabaseManager();
        this.apiClient = bindingService.getApiClient();
    }

    public void setChatListener(JavaChatListener chatListener) {
        this.chatListener = chatListener;
    }

    @EventHandler(priority = EventPriority.HIGHEST)
    public void onPrepareAnvil(PrepareAnvilEvent event) {
        if (!event.getView().getTitle().equals(ANVIL_TITLE)) {
            return;
        }

        AnvilInventory anvil = event.getInventory();
        String text = anvil.getRenameText();
        if (text == null || text.trim().isEmpty()) {
            text = "08";
        }

        ItemStack resultItem = new ItemStack(Material.NAME_TAG);
        ItemMeta meta = resultItem.getItemMeta();
        if (meta != null) {
            meta.setDisplayName(PluginConfig.colorize("&a[ Klik untuk Kirim OTP ]"));
            meta.setLore(List.of(
                    PluginConfig.colorize("&7Nomor: &e" + text),
                    PluginConfig.colorize("&7Klik di sini untuk mengirimkan OTP ke WhatsApp.")
            ));
            resultItem.setItemMeta(meta);
        }

        event.setResult(resultItem);
        anvil.setRepairCost(0);
    }

    @EventHandler(priority = EventPriority.HIGHEST)
    public void onInventoryClick(InventoryClickEvent event) {
        if (!(event.getWhoClicked() instanceof Player player)) {
            return;
        }

        if (event.getView().getType() != InventoryType.ANVIL || !event.getView().getTitle().equals(ANVIL_TITLE)) {
            return;
        }

        // Slot 0 (Kiri): Dummy Item (Cancel agar player tidak mengambilnya)
        if (event.getRawSlot() == 0 || event.getRawSlot() == 1) {
            event.setCancelled(true);
            return;
        }

        // Slot 2 (Kanan): Output Result Slot
        if (event.getRawSlot() == 2) {
            event.setCancelled(true);

            AnvilInventory anvil = (AnvilInventory) event.getInventory();
            String renameText = anvil.getRenameText();

            // Jika renameText kosong, coba ambil dari item display name jika ada
            if (renameText == null || renameText.trim().isEmpty()) {
                ItemStack item0 = anvil.getItem(0);
                if (item0 != null && item0.hasItemMeta() && item0.getItemMeta().hasDisplayName()) {
                    renameText = item0.getItemMeta().getDisplayName();
                }
            }

            if (renameText == null) {
                renameText = "";
            }

            final String rawInput = renameText;
            UUID uuid = player.getUniqueId();

            // Tutup inventory Anvil
            player.closeInventory();

            String normalized = bindingService.normalizePhone(rawInput);
            if (!bindingService.isValidPhone(normalized)) {
                player.sendMessage(config.getMessage("invalid-format"));
                player.sendMessage(PluginConfig.colorize("&7Ketik nomor yang benar di chat atau gunakan &a/bindwa&7 kembali."));
                return;
            }

            long remainingCooldown = bindingService.getCooldownRemainingSeconds(uuid);
            if (remainingCooldown > 0) {
                player.sendMessage(config.getMessage("cooldown", Map.of("seconds", String.valueOf(remainingCooldown))));
                return;
            }

            // Pengecekan database SQLite async
            Bukkit.getScheduler().runTaskAsynchronously(plugin, () -> {
                if (databaseManager.isPhoneTakenByOther(normalized, uuid)) {
                    Bukkit.getScheduler().runTask(plugin, () -> player.sendMessage(config.getMessage("phone-taken")));
                    return;
                }

                databaseManager.saveOrUpdatePendingBinding(uuid, normalized);
                databaseManager.logAction(uuid, normalized, "send_otp");

                apiClient.sendOtp(uuid, normalized).thenAccept(result -> {
                    Bukkit.getScheduler().runTask(plugin, () -> {
                        if (!player.isOnline()) {
                            return;
                        }

                        if (result.isSuccess()) {
                            bindingService.setCooldown(uuid);
                            bindingService.setPendingSession(uuid, normalized);
                            if (chatListener != null) {
                                chatListener.registerPending(uuid, normalized);
                            }
                            player.sendMessage(config.getMessage("otp-sent"));
                        } else if (result.getStatus() == SendOtpResult.Status.COOLDOWN) {
                            long rem = bindingService.getCooldownRemainingSeconds(uuid);
                            if (rem <= 0) rem = config.getOtpCooldownSeconds();
                            player.sendMessage(config.getMessage("cooldown", Map.of("seconds", String.valueOf(rem))));
                        } else if (result.getStatus() == SendOtpResult.Status.INVALID_FORMAT) {
                            player.sendMessage(config.getMessage("invalid-format"));
                        } else {
                            player.sendMessage(config.getMessage("service-unavailable"));
                        }
                    });
                });
            });
        }
    }
}
