package com.dozzy.ui;

import com.dozzy.BindWAPlugin;
import com.dozzy.config.PluginConfig;
import com.dozzy.database.DatabaseManager;
import com.dozzy.http.BotApiClient;
import com.dozzy.http.SendOtpResult;
import com.dozzy.http.VerifyOtpResult;
import com.dozzy.service.BindingService;
import com.dozzy.bridge.ChatBridgeManager;
import com.dozzy.bridge.WAMessageContext;
import org.bukkit.Bukkit;
import org.bukkit.entity.Player;
import org.geysermc.cumulus.form.CustomForm;
import org.geysermc.cumulus.form.SimpleForm;
import org.geysermc.floodgate.api.FloodgateApi;

import java.util.List;
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

    public void openChatMenuForm(Player player, ChatBridgeManager manager) {
        SimpleForm.Builder builder = SimpleForm.builder()
                .title("WhatsApp Chat Bridge")
                .content("Pilih aksi untuk chat ke grup WhatsApp:")
                .button("Kirim Pesan Baru ke WhatsApp")
                .button("Balas Pesan WhatsApp (Reply)");

        builder.validResultHandler(response -> {
            int buttonId = response.clickedButtonId();
            if (buttonId == 0) {
                openSendNewChatForm(player, manager);
            } else if (buttonId == 1) {
                openReplySelectForm(player, manager);
            }
        });

        FloodgateApi.getInstance().sendForm(player.getUniqueId(), builder.build());
    }

    public void openSendNewChatForm(Player player, ChatBridgeManager manager) {
        List<String> knownUsers = manager.getKnownWaUsers();

        CustomForm.Builder builder = CustomForm.builder()
                .title("Kirim Chat ke WhatsApp")
                .label("Tulis pesan yang ingin dikirim ke grup WhatsApp (bisa gunakan @Nama untuk tag):");

        builder.input("Pesan", "Halo semua!");

        if (!knownUsers.isEmpty()) {
            List<String> dropdownOptions = new java.util.ArrayList<>();
            dropdownOptions.add("(Tanpa Tag)");
            for (String user : knownUsers) {
                dropdownOptions.add("@" + user);
            }
            builder.dropdown("Tag Member (Opsional)", dropdownOptions, 0);
        }

        builder.validResultHandler(response -> {
            String msg = response.asInput(1);
            if (msg == null || msg.trim().isEmpty()) {
                player.sendMessage(PluginConfig.colorize("&cPesan tidak boleh kosong."));
                return;
            }

            String finalMsg = msg.trim();
            if (!knownUsers.isEmpty()) {
                int selectedDropdown = response.asDropdown(2);
                if (selectedDropdown > 0 && selectedDropdown <= knownUsers.size()) {
                    String selectedTag = "@" + knownUsers.get(selectedDropdown - 1);
                    if (!finalMsg.contains(selectedTag)) {
                        finalMsg = selectedTag + " " + finalMsg;
                    }
                }
            }

            manager.sendChatMessage(player.getName(), finalMsg);
            player.sendMessage(PluginConfig.colorize("&aChat berhasil dikirim ke grup WhatsApp!"));
        });

        FloodgateApi.getInstance().sendForm(player.getUniqueId(), builder.build());
    }

    public void openReplySelectForm(Player player, ChatBridgeManager manager) {
        List<WAMessageContext> recents = manager.getRecentMessages(10);
        if (recents.isEmpty()) {
            player.sendMessage(PluginConfig.colorize("&7Belum ada pesan WhatsApp terbaru untuk dibalas."));
            return;
        }

        SimpleForm.Builder builder = SimpleForm.builder()
                .title("Pilih Pesan untuk Dibalas")
                .content("Pilih salah satu pesan WhatsApp di bawah ini:");

        for (WAMessageContext ctx : recents) {
            String snippet = ctx.getText();
            if (snippet.length() > 25) {
                snippet = snippet.substring(0, 25) + "...";
            }
            builder.button("§e@" + ctx.getPushName() + "\n§0" + snippet);
        }

        builder.validResultHandler(response -> {
            int index = response.clickedButtonId();
            if (index >= 0 && index < recents.size()) {
                openReplyInputForm(player, manager, recents.get(index));
            }
        });

        FloodgateApi.getInstance().sendForm(player.getUniqueId(), builder.build());
    }

    public void openReplyInputForm(Player player, ChatBridgeManager manager, WAMessageContext ctx) {
        CustomForm.Builder builder = CustomForm.builder()
                .title("Balas Pesan WhatsApp")
                .label("§eMembalas @" + ctx.getPushName() + "§7: §f\"" + ctx.getText() + "\"")
                .input("Pesan Balasan", "Tulis balasanmu...");

        builder.validResultHandler(response -> {
            String replyMsg = response.asInput(1);
            if (replyMsg != null && !replyMsg.trim().isEmpty()) {
                manager.sendReplyMessage(player.getName(), replyMsg.trim(), ctx);
                player.sendMessage(PluginConfig.colorize("&a[WA-Reply] Membalas &e@" + ctx.getPushName() + "&a: &f" + replyMsg.trim()));
            } else {
                player.sendMessage(PluginConfig.colorize("&cPesan balasan tidak boleh kosong."));
            }
        });

        FloodgateApi.getInstance().sendForm(player.getUniqueId(), builder.build());
    }
}
