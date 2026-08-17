package com.dozzy.service;

import com.dozzy.BindWAPlugin;
import com.dozzy.config.PluginConfig;
import com.dozzy.database.DatabaseManager;
import com.dozzy.http.BotApiClient;
import org.bukkit.Bukkit;
import org.bukkit.entity.Player;
import org.geysermc.floodgate.api.FloodgateApi;

import java.util.Map;
import java.util.Optional;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;
import java.util.regex.Pattern;

public class BindingService {

    private final BindWAPlugin plugin;
    private final PluginConfig config;
    private final DatabaseManager databaseManager;
    private final BotApiClient apiClient;

    private final Map<UUID, PendingSession> pendingSessions = new ConcurrentHashMap<>();
    private final Map<UUID, Long> cooldowns = new ConcurrentHashMap<>();
    private Pattern phonePattern;

    public static class PendingSession {
        private final UUID uuid;
        private final String phone;
        private final long createdAt;

        public PendingSession(UUID uuid, String phone) {
            this.uuid = uuid;
            this.phone = phone;
            this.createdAt = System.currentTimeMillis();
        }

        public UUID getUuid() {
            return uuid;
        }

        public String getPhone() {
            return phone;
        }

        public long getCreatedAt() {
            return createdAt;
        }
    }

    public BindingService(BindWAPlugin plugin, PluginConfig config, DatabaseManager databaseManager, BotApiClient apiClient) {
        this.plugin = plugin;
        this.config = config;
        this.databaseManager = databaseManager;
        this.apiClient = apiClient;
        initPattern();
    }

    private void initPattern() {
        String regex = config.getPhoneRegex();
        // Normalisasi regex dari config jika ada double escape
        if (regex == null || regex.isEmpty()) {
            regex = "^62[0-9]{8,13}$";
        } else {
            regex = regex.replace("\\\\d", "[0-9]").replace("\\d", "[0-9]");
        }
        this.phonePattern = Pattern.compile(regex);
    }

    public long getCooldownRemainingSeconds(UUID uuid) {
        Long lastSent = cooldowns.get(uuid);
        if (lastSent == null) {
            return 0;
        }
        long elapsed = (System.currentTimeMillis() - lastSent) / 1000L;
        long cooldownSeconds = config.getOtpCooldownSeconds();
        if (elapsed >= cooldownSeconds) {
            cooldowns.remove(uuid);
            return 0;
        }
        return cooldownSeconds - elapsed;
    }

    public void setCooldown(UUID uuid) {
        cooldowns.put(uuid, System.currentTimeMillis());
    }

    public boolean isBedrockPlayer(UUID uuid) {
        try {
            if (Bukkit.getPluginManager().isPluginEnabled("floodgate")) {
                return FloodgateApi.getInstance().isFloodgatePlayer(uuid);
            }
        } catch (Throwable ignored) {
        }
        return false;
    }

    public String normalizePhone(String rawInput) {
        if (rawInput == null) {
            return "";
        }
        String clean = rawInput.replaceAll("[^0-9]", "");

        // Tangani jika pemain mengetik di AnvilGUI tanpa menghapus default prefix '08'
        if (clean.startsWith("0808")) {
            clean = clean.substring(2);
        } else if (clean.startsWith("0862")) {
            clean = clean.substring(2);
        } else if (clean.startsWith("6262")) {
            clean = clean.substring(2);
        }

        if (clean.startsWith("08")) {
            clean = "628" + clean.substring(2);
        } else if (clean.startsWith("8")) {
            clean = "628" + clean.substring(1);
        } else if (clean.startsWith("0")) {
            clean = "62" + clean.substring(1);
        }
        return clean;
    }

    public boolean isValidPhone(String phone) {
        if (phone == null || phone.isEmpty()) {
            return false;
        }
        if (phonePattern != null && phonePattern.matcher(phone).matches()) {
            return true;
        }
        // Fallback validasi standar format nomor Indonesia 628xxxxxxxx (10-15 digit)
        return phone.matches("^62[0-9]{8,13}$");
    }

    public void setPendingSession(UUID uuid, String phone) {
        pendingSessions.put(uuid, new PendingSession(uuid, phone));
    }

    public Optional<PendingSession> getPendingSession(UUID uuid) {
        PendingSession session = pendingSessions.get(uuid);
        if (session == null) {
            return Optional.empty();
        }
        // Cek jika sesi sudah lebih dari 5 menit (300 detik)
        if (System.currentTimeMillis() - session.getCreatedAt() > 300_000L) {
            pendingSessions.remove(uuid);
            return Optional.empty();
        }
        return Optional.of(session);
    }

    public void removePendingSession(UUID uuid) {
        pendingSessions.remove(uuid);
    }

    public void handleSuccessfulBinding(Player player, String phone) {
        UUID uuid = player.getUniqueId();
        removePendingSession(uuid);

        // 1. Simpan status verified ke database (SQLite)
        databaseManager.setVerified(uuid, phone);
        databaseManager.logAction(uuid, phone, "verify_success");

        // 2. Berikan reward di Main Thread
        Bukkit.getScheduler().runTask(plugin, () -> {
            if (databaseManager.claimReward(uuid)) {
                // Eksekusi semua command reward di console
                for (String command : config.getRewardCommands()) {
                    String formattedCmd = command.replace("%player%", player.getName());
                    Bukkit.dispatchCommand(Bukkit.getConsoleSender(), formattedCmd);
                }
                player.sendMessage(config.getRewardMessage());
            } else {
                player.sendMessage(PluginConfig.colorize("&aNomor WhatsApp kamu berhasil diverifikasi!"));
            }
        });
    }

    public PluginConfig getConfig() {
        return config;
    }

    public DatabaseManager getDatabaseManager() {
        return databaseManager;
    }

    public BotApiClient getApiClient() {
        return apiClient;
    }
}
