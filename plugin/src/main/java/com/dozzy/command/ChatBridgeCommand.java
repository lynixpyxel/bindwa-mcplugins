package com.dozzy.command;

import com.dozzy.bridge.ChatBridgeManager;
import com.dozzy.config.PluginConfig;
import org.bukkit.command.Command;
import org.bukkit.command.CommandExecutor;
import org.bukkit.command.CommandSender;
import org.bukkit.entity.Player;
import org.jetbrains.annotations.NotNull;

public class ChatBridgeCommand implements CommandExecutor {

    private final ChatBridgeManager manager;

    public ChatBridgeCommand(ChatBridgeManager manager) {
        this.manager = manager;
    }

    @Override
    public boolean onCommand(@NotNull CommandSender sender, @NotNull Command command, @NotNull String label, @NotNull String[] args) {
        if (!sender.hasPermission("bindwa.chat")) {
            sender.sendMessage(PluginConfig.colorize("&cKamu tidak memiliki izin untuk menggunakan perintah ini."));
            return true;
        }

        if (args.length == 0) {
            sender.sendMessage(PluginConfig.colorize("&cPenggunaan: /" + label + " <pesan> (contoh: /" + label + " halo semua)"));
            return true;
        }

        String message = String.join(" ", args).trim();
        if (message.isEmpty()) {
            sender.sendMessage(PluginConfig.colorize("&cPesan tidak boleh kosong."));
            return true;
        }

        String senderName = (sender instanceof Player player) ? player.getName() : "Server";

        manager.sendChatMessage(senderName, message);
        sender.sendMessage(PluginConfig.colorize("&aChat berhasil dikirim ke grup WhatsApp!"));

        return true;
    }
}
