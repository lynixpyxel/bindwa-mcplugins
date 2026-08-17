package com.dozzy.ui;

import com.dozzy.BindWAPlugin;
import com.dozzy.config.PluginConfig;
import com.dozzy.listener.JavaChatListener;
import com.dozzy.service.BindingService;
import org.bukkit.entity.Player;

public class JavaAnvilFlow {

    private final BindWAPlugin plugin;
    private final BindingService bindingService;
    private JavaChatListener chatListener;

    public JavaAnvilFlow(BindWAPlugin plugin, BindingService bindingService) {
        this.plugin = plugin;
        this.bindingService = bindingService;
    }

    public void setChatListener(JavaChatListener chatListener) {
        this.chatListener = chatListener;
    }

    public void openPhoneInputAnvil(Player player) {
        // Alur Java murni Chat-based (AnvilGUI dinonaktifkan)
        if (chatListener != null) {
            chatListener.registerPendingPhoneInput(player.getUniqueId());
        }

        player.sendMessage("");
        player.sendMessage(PluginConfig.colorize("&e&l[BindWA] &aSilakan ketik nomor WhatsApp kamu di chat (contoh: &f08123456789&a):"));
        player.sendMessage(PluginConfig.colorize("&7Pesan chat kamu akan dicegat otomatis dan tidak akan terlihat oleh player lain."));
        player.sendMessage(PluginConfig.colorize("&7Ketik &c'batal'&7 untuk membatalkan proses binding."));
        player.sendMessage("");
    }
}
