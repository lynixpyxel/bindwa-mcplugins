package com.dozzy.listener;

import com.dozzy.BindWAPlugin;
import com.dozzy.config.PluginConfig;
import com.dozzy.database.DatabaseManager;
import com.dozzy.http.BotApiClient;
import com.dozzy.http.SendOtpResult;
import com.dozzy.http.VerifyOtpResult;
import com.dozzy.service.BindingService;
import org.bukkit.Bukkit;
import org.bukkit.entity.Player;
import org.bukkit.event.EventHandler;
import org.bukkit.event.EventPriority;
import org.bukkit.event.Listener;
import org.bukkit.event.player.AsyncPlayerChatEvent;
import org.bukkit.event.player.PlayerQuitEvent;

import java.util.Map;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;

public class JavaChatListener implements Listener {

    private final BindWAPlugin plugin;
    private final BindingService bindingService;
    private final PluginConfig config;
    private final DatabaseManager databaseManager;
    private final BotApiClient apiClient;

    private final Map<UUID, PendingChatOTP> pendingChatOtps = new ConcurrentHashMap<>();
    private final Map<UUID, Long> pendingPhoneInputs = new ConcurrentHashMap<>();

    public static class PendingChatOTP {
        private final String phone;
        private final long createdAt;

        public PendingChatOTP(String phone) {
            this.phone = phone;
            this.createdAt = System.currentTimeMillis();
        }

        public String getPhone() {
            return phone;
        }

        public boolean isExpired() {
            // Expire setelah 5 menit (300 detik)
            return System.currentTimeMillis() - createdAt > 300_000L;
        }
    }

    public JavaChatListener(BindWAPlugin plugin, BindingService bindingService) {
        this.plugin = plugin;
        this.bindingService = bindingService;
        this.config = bindingService.getConfig();
        this.databaseManager = bindingService.getDatabaseManager();
        this.apiClient = bindingService.getApiClient();
    }

    public void registerPendingPhoneInput(UUID uuid) {
        pendingPhoneInputs.put(uuid, System.currentTimeMillis());
    }

    public void unregisterPendingPhoneInput(UUID uuid) {
        pendingPhoneInputs.remove(uuid);
    }

    public void registerPending(UUID uuid, String phone) {
        pendingPhoneInputs.remove(uuid);
        pendingChatOtps.put(uuid, new PendingChatOTP(phone));
    }

    public void unregisterPending(UUID uuid) {
        pendingPhoneInputs.remove(uuid);
        pendingChatOtps.remove(uuid);
    }

    public boolean isPending(UUID uuid) {
        return pendingPhoneInputs.containsKey(uuid) || pendingChatOtps.containsKey(uuid);
    }

    @EventHandler(priority = EventPriority.HIGHEST)
    public void onPlayerChat(AsyncPlayerChatEvent event) {
        Player player = event.getPlayer();
        UUID uuid = player.getUniqueId();

        // 1. Cek apakah pemain sedang dalam sesi input nomor telepon (fallback chat flow)
        if (pendingPhoneInputs.containsKey(uuid)) {
            event.setCancelled(true);
            handlePhoneChatInput(player, event.getMessage().trim());
            return;
        }

        // 2. Cek apakah pemain sedang dalam sesi input OTP
        PendingChatOTP pendingOtp = pendingChatOtps.get(uuid);
        if (pendingOtp != null) {
            event.setCancelled(true);
            handleOtpChatInput(player, pendingOtp, event.getMessage().trim());
        }
    }

    private void handlePhoneChatInput(Player player, String rawMessage) {
        UUID uuid = player.getUniqueId();

        if (rawMessage.equalsIgnoreCase("cancel") || rawMessage.equalsIgnoreCase("batal")) {
            unregisterPendingPhoneInput(uuid);
            player.sendMessage(config.getMessage("cancelled"));
            return;
        }

        String normalized = bindingService.normalizePhone(rawMessage);
        if (!bindingService.isValidPhone(normalized)) {
            player.sendMessage(config.getMessage("invalid-format"));
            player.sendMessage(PluginConfig.colorize("&7Ketik nomor yang benar atau ketik &c'batal'&7 untuk membatalkan."));
            return;
        }

        long remainingCooldown = bindingService.getCooldownRemainingSeconds(uuid);
        if (remainingCooldown > 0) {
            player.sendMessage(config.getMessage("cooldown", Map.of("seconds", String.valueOf(remainingCooldown))));
            return;
        }

        // Cek keunikan nomor di SQLite secara async
        Bukkit.getScheduler().runTaskAsynchronously(plugin, () -> {
            if (databaseManager.isPhoneTakenByOther(normalized, uuid)) {
                Bukkit.getScheduler().runTask(plugin, () -> player.sendMessage(config.getMessage("phone-taken")));
                return;
            }

            databaseManager.saveOrUpdatePendingBinding(uuid, normalized);
            databaseManager.logAction(uuid, normalized, "send_otp");

            apiClient.sendOtp(uuid, player.getName(), normalized).thenAccept(result -> {
                Bukkit.getScheduler().runTask(plugin, () -> {
                    if (!player.isOnline()) {
                        return;
                    }

                    if (result.isSuccess()) {
                        bindingService.setCooldown(uuid);
                        bindingService.setPendingSession(uuid, normalized);
                        registerPending(uuid, normalized);
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

    private void handleOtpChatInput(Player player, PendingChatOTP pending, String rawMessage) {
        UUID uuid = player.getUniqueId();

        if (pending.isExpired()) {
            unregisterPending(uuid);
            player.sendMessage(config.getMessage("otp-expired"));
            return;
        }

        if (rawMessage.equalsIgnoreCase("cancel") || rawMessage.equalsIgnoreCase("batal")) {
            unregisterPending(uuid);
            player.sendMessage(config.getMessage("cancelled"));
            return;
        }

        String phone = pending.getPhone();
        String otpInput = rawMessage.replaceAll("\\s+", "");

        // Panggil verify-otp ke bot secara async
        apiClient.verifyOtp(uuid, player.getName(), phone, otpInput).thenAccept(result -> {
            Bukkit.getScheduler().runTask(plugin, () -> {
                if (!player.isOnline()) {
                    return;
                }

                if (result.isVerified()) {
                    // Verifikasi Berhasil
                    unregisterPending(uuid);
                    bindingService.handleSuccessfulBinding(player, phone);
                } else if (result.getStatus() == VerifyOtpResult.Status.WRONG_OTP) {
                    // OTP Salah -> log & beri tahu sisa percobaan
                    databaseManager.logAction(uuid, phone, "verify_fail");
                    String msg = config.getMessage("otp-wrong", Map.of("attempts", String.valueOf(result.getAttemptsLeft())));
                    player.sendMessage(msg);
                } else if (result.getStatus() == VerifyOtpResult.Status.MAX_ATTEMPTS_EXCEEDED) {
                    // Batas percobaan habis
                    unregisterPending(uuid);
                    databaseManager.logAction(uuid, phone, "verify_fail");
                    player.sendMessage(config.getMessage("max-attempts"));
                } else if (result.getStatus() == VerifyOtpResult.Status.EXPIRED_OR_NOT_FOUND) {
                    // OTP Expired
                    unregisterPending(uuid);
                    player.sendMessage(config.getMessage("otp-expired"));
                } else {
                    // Error lainnya / Bot down
                    player.sendMessage(config.getMessage("service-unavailable"));
                }
            });
        });
    }

    @EventHandler
    public void onPlayerQuit(PlayerQuitEvent event) {
        unregisterPending(event.getPlayer().getUniqueId());
        bindingService.removePendingSession(event.getPlayer().getUniqueId());
    }
}
