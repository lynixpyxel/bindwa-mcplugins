package com.dozzy.ui;

import com.dozzy.BindWAPlugin;
import com.dozzy.config.PluginConfig;
import com.dozzy.database.DatabaseManager;
import com.dozzy.http.BotApiClient;
import com.dozzy.http.SendOtpResult;
import com.dozzy.http.VerifyOtpResult;
import com.dozzy.service.BindingService;
import org.bukkit.Bukkit;
import org.bukkit.entity.Player;
import org.geysermc.cumulus.form.CustomForm;
import org.geysermc.floodgate.api.FloodgateApi;

import java.util.Map;
import java.util.UUID;

public class BedrockFormFlow {

    private final BindWAPlugin plugin;
    private final BindingService bindingService;

    public BedrockFormFlow(BindWAPlugin plugin, BindingService bindingService) {
        this.plugin = plugin;
        this.bindingService = bindingService;
    }

    public void openPhoneInputForm(Player player) {
        openPhoneInputForm(player, null);
    }

    public void openPhoneInputForm(Player player, String errorMessage) {
        PluginConfig config = bindingService.getConfig();
        DatabaseManager db = bindingService.getDatabaseManager();
        BotApiClient apiClient = bindingService.getApiClient();
        UUID uuid = player.getUniqueId();

        CustomForm.Builder builder = CustomForm.builder()
                .title("WhatsApp Binding");

        if (errorMessage != null && !errorMessage.isEmpty()) {
            builder.label(errorMessage);
        } else {
            builder.label("Hubungkan nomor WhatsApp kamu untuk mendapatkan reward eksklusif in-game!");
        }

        builder.input("Nomor WhatsApp", "08123456789", "08");

        builder.validResultHandler(response -> {
            String rawPhone = response.asInput(1);
            if (rawPhone == null || rawPhone.trim().isEmpty()) {
                openPhoneInputForm(player, "§cNomor WhatsApp tidak boleh kosong.");
                return;
            }

            String normalized = bindingService.normalizePhone(rawPhone);
            if (!bindingService.isValidPhone(normalized)) {
                openPhoneInputForm(player, "§cFormat nomor salah. Gunakan format 08xxxxxxxx atau 628xxxxxxxx");
                return;
            }

            long remainingCooldown = bindingService.getCooldownRemainingSeconds(uuid);
            if (remainingCooldown > 0) {
                openPhoneInputForm(player, "§cMohon tunggu " + remainingCooldown + " detik sebelum meminta OTP kembali.");
                return;
            }

            // Cek ketersediaan nomor di database SQLite secara async
            Bukkit.getScheduler().runTaskAsynchronously(plugin, () -> {
                if (db.isPhoneTakenByOther(normalized, uuid)) {
                    Bukkit.getScheduler().runTask(plugin, () -> openPhoneInputForm(player, "§cNomor ini sudah terdaftar di akun lain."));
                    return;
                }

                // Simpan pending dan panggil send-otp
                db.saveOrUpdatePendingBinding(uuid, normalized);
                db.logAction(uuid, normalized, "send_otp");

                apiClient.sendOtp(uuid, normalized).thenAccept(result -> {
                    Bukkit.getScheduler().runTask(plugin, () -> {
                        if (!player.isOnline()) {
                            return;
                        }

                        if (result.isSuccess()) {
                            bindingService.setCooldown(uuid);
                            bindingService.setPendingSession(uuid, normalized);
                            player.sendMessage(config.getMessage("otp-sent"));
                            openOtpInputForm(player, normalized, null);
                        } else if (result.getStatus() == SendOtpResult.Status.COOLDOWN) {
                            long rem = bindingService.getCooldownRemainingSeconds(uuid);
                            if (rem <= 0) rem = config.getOtpCooldownSeconds();
                            player.sendMessage(config.getMessage("cooldown", Map.of("seconds", String.valueOf(rem))));
                        } else if (result.getStatus() == SendOtpResult.Status.INVALID_FORMAT) {
                            openPhoneInputForm(player, "§cFormat nomor tidak valid.");
                        } else {
                            player.sendMessage(config.getMessage("service-unavailable"));
                        }
                    });
                });
            });
        });

        builder.closedOrInvalidResultHandler(() -> {
            // Player menutup form tanpa submit -> tidak ada efek samping
        });

        FloodgateApi.getInstance().sendForm(uuid, builder.build());
    }

    public void openOtpInputForm(Player player, String phone, String errorMessage) {
        PluginConfig config = bindingService.getConfig();
        DatabaseManager db = bindingService.getDatabaseManager();
        BotApiClient apiClient = bindingService.getApiClient();
        UUID uuid = player.getUniqueId();

        CustomForm.Builder builder = CustomForm.builder()
                .title("Verifikasi OTP WhatsApp");

        if (errorMessage != null && !errorMessage.isEmpty()) {
            builder.label(errorMessage);
        } else {
            builder.label("Kode verifikasi OTP 6-digit telah dikirim ke nomor WhatsApp Anda.");
        }

        builder.input("Kode OTP (6 Digit)", "123456");

        builder.validResultHandler(response -> {
            String otpInput = response.asInput(1);
            if (otpInput == null || otpInput.trim().isEmpty()) {
                openOtpInputForm(player, phone, "§cKode OTP tidak boleh kosong.");
                return;
            }

            String cleanOtp = otpInput.trim().replaceAll("\\s+", "");

            // Panggil verify-otp ke bot secara async
            apiClient.verifyOtp(uuid, phone, cleanOtp).thenAccept(result -> {
                Bukkit.getScheduler().runTask(plugin, () -> {
                    if (!player.isOnline()) {
                        return;
                    }

                    if (result.isVerified()) {
                        // Verifikasi Berhasil
                        bindingService.handleSuccessfulBinding(player, phone);
                    } else if (result.getStatus() == VerifyOtpResult.Status.WRONG_OTP) {
                        // OTP Salah -> Log dan buka form kembali dengan pesan sisa percobaan
                        db.logAction(uuid, phone, "verify_fail");
                        String err = "§cKode OTP salah! Sisa percobaan: " + result.getAttemptsLeft();
                        openOtpInputForm(player, phone, err);
                    } else if (result.getStatus() == VerifyOtpResult.Status.MAX_ATTEMPTS_EXCEEDED) {
                        // Batas percobaan habis
                        db.logAction(uuid, phone, "verify_fail");
                        player.sendMessage(config.getMessage("max-attempts"));
                    } else if (result.getStatus() == VerifyOtpResult.Status.EXPIRED_OR_NOT_FOUND) {
                        // OTP Expired
                        player.sendMessage(config.getMessage("otp-expired"));
                    } else {
                        // Layanan error / down
                        player.sendMessage(config.getMessage("service-unavailable"));
                    }
                });
            });
        });

        builder.closedOrInvalidResultHandler(() -> {
            // Player menutup form -> pending session tetap ada hingga 5 menit
        });

        FloodgateApi.getInstance().sendForm(uuid, builder.build());
    }
}
