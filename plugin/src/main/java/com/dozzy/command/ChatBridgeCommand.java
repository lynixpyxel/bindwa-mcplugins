package com.dozzy.command;

import com.dozzy.BindWAPlugin;
import com.dozzy.bridge.ChatBridgeManager;
import com.dozzy.bridge.WAMessageContext;
import com.dozzy.config.PluginConfig;
import com.dozzy.ui.BedrockFormFlow;
import org.bukkit.Bukkit;
import org.bukkit.command.Command;
import org.bukkit.command.CommandExecutor;
import org.bukkit.command.CommandSender;
import org.bukkit.command.TabCompleter;
import org.bukkit.entity.Player;
import org.geysermc.floodgate.api.FloodgateApi;
import org.jetbrains.annotations.NotNull;
import org.jetbrains.annotations.Nullable;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;

public class ChatBridgeCommand implements CommandExecutor, TabCompleter {

    private final BindWAPlugin plugin;
    private final ChatBridgeManager manager;
    private final BedrockFormFlow bedrockFormFlow;

    public ChatBridgeCommand(BindWAPlugin plugin, ChatBridgeManager manager, BedrockFormFlow bedrockFormFlow) {
        this.plugin = plugin;
        this.manager = manager;
        this.bedrockFormFlow = bedrockFormFlow;
    }

    private boolean isFloodgatePlayer(Player player) {
        if (!Bukkit.getPluginManager().isPluginEnabled("floodgate")) {
            return false;
        }
        try {
            return FloodgateApi.getInstance().isFloodgatePlayer(player.getUniqueId());
        } catch (Throwable ignored) {
            return false;
        }
    }

    @Override
    public boolean onCommand(@NotNull CommandSender sender, @NotNull Command command, @NotNull String label, @NotNull String[] args) {
        if (!sender.hasPermission("bindwa.chat")) {
            sender.sendMessage(PluginConfig.colorize("&cKamu tidak memiliki izin untuk menggunakan perintah ini."));
            return true;
        }

        // Jika player Bedrock mengetik /chat tanpa argumen, buka Bedrock UI Form
        if (args.length == 0) {
            if (sender instanceof Player player && isFloodgatePlayer(player) && bedrockFormFlow != null) {
                bedrockFormFlow.openChatMenuForm(player, manager);
                return true;
            }

            sender.sendMessage(PluginConfig.colorize("&cPenggunaan: /" + label + " <pesan> atau /" + label + " reply <id> <pesan>"));
            return true;
        }

        String senderName = (sender instanceof Player player) ? player.getName() : "Server";

        // Cek subcommand reply: /chat reply <id> <pesan...> atau /chat -r <id> <pesan...>
        if (args[0].equalsIgnoreCase("reply") || args[0].equalsIgnoreCase("-r")) {
            if (args.length == 1) {
                if (sender instanceof Player player && isFloodgatePlayer(player) && bedrockFormFlow != null) {
                    bedrockFormFlow.openReplySelectForm(player, manager);
                    return true;
                }
                sender.sendMessage(PluginConfig.colorize("&cPenggunaan: /" + label + " reply <id> <pesan>"));
                return true;
            }

            if (args.length < 3) {
                sender.sendMessage(PluginConfig.colorize("&cPenggunaan: /" + label + " reply <id> <pesan>"));
                return true;
            }

            String msgId = args[1];
            String replyMessage = String.join(" ", Arrays.copyOfRange(args, 2, args.length)).trim();

            if (replyMessage.isEmpty()) {
                sender.sendMessage(PluginConfig.colorize("&cPesan balasan tidak boleh kosong."));
                return true;
            }

            WAMessageContext ctx = manager.getMessageContext(msgId);
            if (ctx == null) {
                sender.sendMessage(PluginConfig.colorize("&cPesan dengan ID '&e" + msgId + "&c' tidak ditemukan atau sudah kadaluarsa."));
                return true;
            }

            manager.sendReplyMessage(senderName, replyMessage, ctx);
            sender.sendMessage(PluginConfig.colorize("&a[WA-Reply] Membalas &e@" + ctx.getPushName() + "&a: &f" + replyMessage));
            return true;
        }

        String message = String.join(" ", args).trim();
        if (message.isEmpty()) {
            sender.sendMessage(PluginConfig.colorize("&cPesan tidak boleh kosong."));
            return true;
        }

        manager.sendChatMessage(senderName, message);
        sender.sendMessage(PluginConfig.colorize("&aChat berhasil dikirim ke grup WhatsApp!"));

        return true;
    }

    @Override
    public @Nullable List<String> onTabComplete(@NotNull CommandSender sender, @NotNull Command command, @NotNull String label, @NotNull String[] args) {
        List<String> completions = new ArrayList<>();
        if (!sender.hasPermission("bindwa.chat")) {
            return completions;
        }

        if (args.length == 1) {
            String current = args[0].toLowerCase();
            if ("reply".startsWith(current)) {
                completions.add("reply");
            }
            for (String user : manager.getKnownWaUsers()) {
                String tag = "@" + user;
                if (tag.toLowerCase().startsWith(current) || current.startsWith("@")) {
                    completions.add(tag);
                }
            }
            return completions;
        }

        if (args.length == 2 && (args[0].equalsIgnoreCase("reply") || args[0].equalsIgnoreCase("-r"))) {
            String current = args[1].toLowerCase();
            for (WAMessageContext ctx : manager.getRecentMessages(10)) {
                if (ctx.getShortId().toLowerCase().startsWith(current)) {
                    completions.add(ctx.getShortId());
                }
            }
            return completions;
        }

        // Jika sedang mengetik pesan dan kata terakhir diawali '@'
        String lastWord = args[args.length - 1];
        if (lastWord.startsWith("@")) {
            String current = lastWord.toLowerCase();
            for (String user : manager.getKnownWaUsers()) {
                String tag = "@" + user;
                if (tag.toLowerCase().startsWith(current)) {
                    completions.add(tag);
                }
            }
        }

        return completions;
    }
}
