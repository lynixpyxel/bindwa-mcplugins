package com.dozzy.config;

import org.bukkit.ChatColor;
import org.bukkit.configuration.file.FileConfiguration;

import java.util.Collections;
import java.util.List;
import java.util.Map;

public class PluginConfig {

    private final String apiBaseUrl;
    private final String apiToken;
    private final String phoneRegex;
    private final int otpCooldownSeconds;
    private final int otpMaxAttempts;
    private final List<String> rewardCommands;
    private final String rewardMessage;
    private final FileConfiguration config;

    public PluginConfig(FileConfiguration config) {
        this.config = config;
        this.apiBaseUrl = config.getString("api.base-url", "http://192.168.18.67:3636");
        this.apiToken = config.getString("api.token", "");
        this.phoneRegex = config.getString("phone.regex", "^62\\d{8,13}$");
        this.otpCooldownSeconds = config.getInt("otp.cooldown-seconds", 60);
        this.otpMaxAttempts = config.getInt("otp.max-attempts", 5);
        this.rewardCommands = config.getStringList("reward.commands");
        this.rewardMessage = colorize(config.getString("reward.message", "&aSelamat! Nomor WA berhasil di-bind, reward telah diberikan!"));
    }

    public String getApiBaseUrl() {
        return apiBaseUrl;
    }

    public String getApiToken() {
        return apiToken;
    }

    public String getPhoneRegex() {
        return phoneRegex;
    }

    public int getOtpCooldownSeconds() {
        return otpCooldownSeconds;
    }

    public int getOtpMaxAttempts() {
        return otpMaxAttempts;
    }

    public List<String> getRewardCommands() {
        return Collections.unmodifiableList(rewardCommands);
    }

    public String getRewardMessage() {
        return rewardMessage;
    }

    public String getMessage(String key) {
        return getMessage(key, Collections.emptyMap());
    }

    public String getMessage(String key, Map<String, String> placeholders) {
        String msg = config.getString("messages." + key, "");
        if (msg.isEmpty()) {
            return "";
        }
        for (Map.Entry<String, String> entry : placeholders.entrySet()) {
            msg = msg.replace("%" + entry.getKey() + "%", entry.getValue());
        }
        return colorize(msg);
    }

    public boolean isChatBridgeEnabled() {
        return config.getBoolean("chat-bridge.enabled", true);
    }

    public boolean isNotificationEnderDragon() {
        return config.getBoolean("chat-bridge.notifications.ender-dragon", true);
    }

    public boolean isNotificationDragonEgg() {
        return config.getBoolean("chat-bridge.notifications.dragon-egg", true);
    }

    public boolean isNotificationElytra() {
        return config.getBoolean("chat-bridge.notifications.elytra", true);
    }

    public static String colorize(String message) {
        if (message == null) {
            return "";
        }
        return ChatColor.translateAlternateColorCodes('&', message);
    }
}
