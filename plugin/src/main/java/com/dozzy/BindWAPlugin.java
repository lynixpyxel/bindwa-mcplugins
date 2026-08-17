package com.dozzy;

import com.dozzy.command.BindWACommand;
import com.dozzy.config.PluginConfig;
import com.dozzy.database.DatabaseManager;
import com.dozzy.http.BotApiClient;
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

    @Override
    public void onEnable() {
        instance = this;

        // 1. Simpan default config jika belum ada
        saveDefaultConfig();
        reloadPluginConfig();

        // 2. Inisialisasi Database SQLite
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

        // 4. Registrasi Command
        if (getCommand("bindwa") != null) {
            getCommand("bindwa").setExecutor(new BindWACommand(this, this.bindingService, this.javaAnvilFlow, this.bedrockFormFlow));
        }

        getLogger().info("BindWA Plugin berhasil diaktifkan! (Versi: " + getDescription().getVersion() + ")");
    }

    @Override
    public void onDisable() {
        if (this.databaseManager != null) {
            this.databaseManager.close();
        }
        instance = null;
        getLogger().info("BindWA Plugin berhasil dinonaktifkan.");
    }

    public void reloadPluginConfig() {
        reloadConfig();
        this.pluginConfig = new PluginConfig(getConfig());
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
}
