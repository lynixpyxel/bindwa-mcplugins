package com.dozzy;

import com.dozzy.bridge.ChatBridgeManager;
import com.dozzy.command.BindWACommand;
import com.dozzy.command.ChatBridgeCommand;
import com.dozzy.config.PluginConfig;
import com.dozzy.database.DatabaseManager;
import com.dozzy.http.BotApiClient;
import com.dozzy.listener.GameBridgeListener;
import com.dozzy.listener.JavaChatListener;
import com.dozzy.service.BindingService;
import com.dozzy.ui.BedrockFormFlow;
import com.dozzy.ui.JavaAnvilFlow;
import org.bukkit.plugin.java.JavaPlugin;

import java.sql.SQLException;
import java.util.logging.Level;

public class BindWAPlugin extends JavaPlugin {

    private static BindWAPlugin instance;
    private PluginConfig pluginConfig;
    private DatabaseManager databaseManager;
    private BotApiClient botApiClient;
    private BindingService bindingService;
    private JavaAnvilFlow javaAnvilFlow;
    private BedrockFormFlow bedrockFormFlow;
    private ChatBridgeManager chatBridgeManager;

    @Override
    public void onEnable() {
        instance = this;

        // 1. Simpan default config jika belum ada
        saveDefaultConfig();
        reloadPluginConfig();

        // 2. Inisialisasi Database SQLite & JSON Sync
        this.databaseManager = new DatabaseManager(getDataFolder(), getLogger());
        try {
            this.databaseManager.initialize();
        } catch (SQLException e) {
            getLogger().log(Level.SEVERE, "Gagal menginisialisasi database SQLite! Plugin akan dinonaktifkan.", e);
            getServer().getPluginManager().disablePlugin(this);
            return;
        }

        // 3. Inisialisasi Services & UI Flows
        this.botApiClient = new BotApiClient(this.pluginConfig, getLogger());
        this.bindingService = new BindingService(this, this.pluginConfig, this.databaseManager, this.botApiClient);
        this.javaAnvilFlow = new JavaAnvilFlow(this, this.bindingService);
        this.bedrockFormFlow = new BedrockFormFlow(this, this.bindingService);

        JavaChatListener javaChatListener = new JavaChatListener(this, this.bindingService);
        this.javaAnvilFlow.setChatListener(javaChatListener);
        getServer().getPluginManager().registerEvents(javaChatListener, this);

        // 4. Inisialisasi Direct WhatsApp Chat Bridge
        this.chatBridgeManager = new ChatBridgeManager(this, this.pluginConfig, this.botApiClient);
        this.chatBridgeManager.start();

        GameBridgeListener gameBridgeListener = new GameBridgeListener(this, this.chatBridgeManager);
        getServer().getPluginManager().registerEvents(gameBridgeListener, this);

        // 5. Registrasi Command
        if (getCommand("bindwa") != null) {
            getCommand("bindwa").setExecutor(new BindWACommand(this, this.bindingService, this.javaAnvilFlow, this.bedrockFormFlow));
        }

        ChatBridgeCommand chatBridgeCommand = new ChatBridgeCommand(this.chatBridgeManager);
        if (getCommand("chat") != null) {
            getCommand("chat").setExecutor(chatBridgeCommand);
        }
        if (getCommand("wa") != null) {
            getCommand("wa").setExecutor(chatBridgeCommand);
        }

        com.dozzy.command.ElytraCommand elytraCommand = new com.dozzy.command.ElytraCommand(this, this.databaseManager);
        if (getCommand("elytraboard") != null) {
            getCommand("elytraboard").setExecutor(elytraCommand);
        }
        if (getCommand("elytracheck") != null) {
            getCommand("elytracheck").setExecutor(elytraCommand);
        }

        getLogger().info("BindWA Plugin + WhatsApp Chat Bridge + Elytra Tracker berhasil diaktifkan! (Versi: " + getDescription().getVersion() + ")");
    }

    @Override
    public void onDisable() {
        if (this.chatBridgeManager != null) {
            this.chatBridgeManager.stop();
        }
        if (this.databaseManager != null) {
            this.databaseManager.close();
        }
        instance = null;
        getLogger().info("BindWA Plugin berhasil dinonaktifkan.");
    }

    public void reloadPluginConfig() {
        reloadConfig();
        this.pluginConfig = new PluginConfig(getConfig());
        if (this.chatBridgeManager != null) {
            this.chatBridgeManager.stop();
            this.chatBridgeManager = new ChatBridgeManager(this, this.pluginConfig, this.botApiClient);
            this.chatBridgeManager.start();
        }
    }

    public static BindWAPlugin getInstance() {
        return instance;
    }

    public PluginConfig getPluginConfig() {
        return pluginConfig;
    }

    public DatabaseManager getDatabaseManager() {
        return databaseManager;
    }

    public ChatBridgeManager getChatBridgeManager() {
        return chatBridgeManager;
    }
}
