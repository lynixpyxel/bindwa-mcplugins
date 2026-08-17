ll.registerPlugin("WAtoMCChatBridge", "WebSocket server for WhatsApp chat bridge", [1, 0, 0]);
const WebSocket = require('ws');
const wsc = new WSClient();
const tar = 'ws://103.151.140.122:7026';

const wss = new WebSocket.Server({ port: 8084 });
let waConn = null;

wss.on('connection', function connection(ws) {
    waConn = 1;
    log("WhatsApp client connected");
    mc.runcmd(`tellraw @a {"rawtext":[{"text":"§aKoneksi ke bot WhatsApp tersambung kembali! Silahkan ketik .chat untuk mengirim chat ke grup (contoh: .chat tes)"}]}`);

    ws.on('message', function incoming(msg) {
        try {
            const message = JSON.parse(msg);
            let formattedMsg = ``;
            let grup = "";

            if (message.group === '6288287243319-1620284343@g.us') {
                formattedMsg = `§b|§aGrup WA Utama CSMP§b| <§a${message.pushName}§b> (§a${message.participant}§b):§r ${message.text}\n§6§otulis '.chat blablabla' buat ngirim chat ke grup WA utama dan cadangan CSMP`;
                grup = "Grup WA Utama CSMP";
            } else {
                formattedMsg = `§b|§aGrup WA Atmin CSMP§b| <§a${message.pushName}§b> (§a${message.participant}§b):§r ${message.text}`;
                grup = "Grup WA Atmin CSMP";
            }

            log("WhatsApp: " + formattedMsg);
            mc.runcmd(`tellraw @a {"rawtext":[{"text":"${formattedMsg.replace(/"/g, '\\"')}"}]}`);

            if (wsc.status === 0) {
                wsc.send(JSON.stringify({
                    type: 'chatWA',
                    message: message.rawMsg,
                    name: message.pushName,
                    number: message.participant,
                    pp: message.pp,
                    group: grup,
                    ppGroup: message.ppGrup
                }));
                log("§aChat dikirim ke channel #server-chat Discord!");
            } else {
                log("§cChat gagal dikirim ke Discord!");
            }
        } catch (error) {
            log("Error processing incoming message: " + error);
        }
    });

    ws.on('close', function () {
        waConn = null;
        log("WhatsApp client disconnected");
        mc.runcmd(`tellraw @a {"rawtext":[{"text":"§cKoneksi ke bot WhatsApp terputus!"}]}`);
    });

    const pingInterval = setInterval(() => {
        if (ws.readyState === WebSocket.OPEN) {
            ws.ping();
        }
    }, 30000);
});

mc.listen("onChat", (player, msg) => {
    if (msg.toLowerCase().startsWith(".wa ")) {
        try {
            if (wss && wss.clients && waConn === 1) {
                const sanitizedMsg = msg.replace(/\.wa /i, "").replace(/"/g, '\\"');
                wss.clients.forEach(client => {
                    client.send(`*|Server|* <${player.realName}>: ${sanitizedMsg}`);
                });
                mc.broadcast("§aChat dikirim ke grup WhatsApp!");
            } else {
                mc.broadcast("§cChat gagal dikirim ke grup WhatsApp!");
            }

            if (wsc.status === 0) {
                wsc.send(JSON.stringify({
                    type: 'chat',
                    message: msg.replace(/\.wa /i, ""),
                    player: player.realName
                }));
                mc.broadcast("§aChat dikirim ke channel #server-chat Discord!");
            } else {
                mc.broadcast("§cChat gagal dikirim ke Discord!");
            }
        } catch (error) {
            mc.broadcast("§cChat gagal dikirim ke grup WhatsApp/Discord!");
        }
    } else if (msg.toLowerCase().startsWith(".chat ")) {
        try {
            if (wss && wss.clients && waConn === 1) {
                const sanitizedMsg = msg.replace(/\.chat /i, "").replace(/"/g, '\\"');
                wss.clients.forEach(client => {
                    client.send(`*|Server|* <${player.realName}>: ${sanitizedMsg}`);
                });
                mc.broadcast("§aChat dikirim ke grup WhatsApp!");
            } else {
                mc.broadcast("§cChat gagal dikirim ke grup WhatsApp!");
            }

            if (wsc.status === 0) {
                wsc.send(JSON.stringify({
                    type: 'chat',
                    message: msg.replace(/\.chat /i, ""),
                    player: player.realName
                }));
                mc.broadcast("§aChat dikirim ke channel #server-chat Discord!");
            } else {
                mc.broadcast("§cChat gagal dikirim ke Discord!");
            }
        } catch (error) {
            mc.broadcast("§cChat gagal dikirim ke grup WhatsApp/Discord!");
        }
    }
});

let reconnecting = false;

function connStat() {
    switch (wsc.status) {
        case 0:
            logger.info('Berhasil menyambung ke websocket!');
            let type = 'serverstart';

            wsc.send(JSON.stringify({ type }));
            reconnecting = false; // Reset flag setelah berhasil tersambung
            break;

        case 1:
            logger.info('Server connect close');
            reconnecting = false; // Reset flag ketika koneksi ditutup
            break;

        case 2:
            logger.info('Gagal menyambungkan');
            logger.info('Mencoba kembali...');
            if (!reconnecting) {
                reconnecting = true; // Set flag saat mencoba menyambung
                setTimeout(() => {
                    if (wsc.status != 0) {
                        logger.info('Status: ', wsc.connectAsync(tar, function (success) {
                            if (success) {
                                log("Connected to Discord bot");
                                mc.broadcast("§bTersambung ke bot Discord!");
                            } else {
                                log("Disconnected from Discord bot");
                            }
                        }), wsc.status);
                        connStat();
                    } else {
                        logger.info('Berhasil tersambung ke websocket');
                    }
                    reconnecting = false; // Reset flag setelah percobaan
                }, 10 * 1000);
            }
            break;

        default:
            logger.info('Status koneksi tidak diketahui');
            reconnecting = false; // Reset flag untuk kasus default
    }
}

setInterval(() => {
    if (wsc.status == 2 && !reconnecting) {
        //logger.info('Status: ', wsc.connectAsync(tar, function(success){}), wsc.status);
        connStat();
    }
}, 1000 * 10);

wsc.listen('onTextReceived', function (msg) {
    const Message = toJSON(msg);
    log(Message);
    switch (Message.type) {
        case 'message':
            mc.runcmdEx(`tellraw @a {"rawtext":[{"text":"§d[§bDiscord§d] §e${Message.sender}: ${Message.gomsg}"}]}`);
            colorLog('blue', `[Discord] ${Message.sender}: ${Message.rawMsg}`)
            if (wss && wss.clients) {
                wss.clients.forEach(client => {
                    client.send("*|Discord CSMP|* <" + Message.sender + ">: " + Message.rawMsg);
                });
                log('Discord chat sent to WhatsApp: ' + Message.rawMsg);
            } else {
                log("Unable to send chat from Discord to WhatsApp!");
            }
            break;
        case 'Playerlist':
            var result = mc.runcmdEx("list");
            let txt = result.output.replace('There are', 'There are')
            txt = txt.replace('players online:', 'player online:')

            wsc.send(JSON.stringify({
                type: 'Playerlist',
                list: txt,
            }))
            break;
        case 'tps':
            var servertps = mc.runcmdEx('tps');
            if (servertps.success) {
                let servertpss = servertps.output.replace('TPS:', 'Server TPS:');

                wsc.send(JSON.stringify({
                    type: 'tps',
                    tps: servertpss,
                }));
            }
            break;
    }
});

wsc.listen('onLostConnection', function (code) {
    log('Disconnected from Discord bot: ' + code);
    mc.broadcast("§cKoneksi ke bot Discord terputus!");
});

wsc.listen('onError', function (msg) {
    log('Error on Discord bot connection: ' + msg);
    mc.broadcast("§cError pada koneksi bot Discord!");
});

function toJSON(str) {
    if (typeof str == 'string') {
        try {
            var obj = JSON.parse(str);
            if (typeof obj == 'object' && obj) { return obj; } else { return false; }
        } catch (e) { }
    }
}

mc.listen(`onMobDie`, (en, src, cse) => {
    if (en.type == "minecraft:ender_dragon") {
        const mobName = "Ender Dragon";
        /* try {
            wss.clients.forEach(client => {
                if (src != null && src.isPlayer()) {
                    const pl = src.toPlayer();
                    client.send("*Ender Dragon telah dibunuh oleh " + pl.realName + "!*");
                } else {
                    client.send("*Ender Dragon berhasil dikalahkan!*");
                }
            });
            //mc.broadcast("§aChat dikirim ke grup!");
            log("Dragon Death Notif Sent, cause int: " + cse);
        } catch (error) {
            //mc.broadcast("§cChat gagal dikirim ke grup!");
            log("Error in Sending Dragon Death Notif: " + error + ", cause int: " + cse);
        } */
        try {
            if (wss && wss.clients && waConn == 1) {
                wss.clients.forEach(client => {
                    if (src != null && src.isPlayer()) {
                        const pl = src.toPlayer();
                        client.send("*" + mobName + " telah dibunuh oleh " + pl.realName + "!*");
                    } else {
                        client.send("*" + mobName + " berhasil dikalahkan!*");
                    }
                });
                log("Dragon Death Notif has been sent to WhatsApp, cause int: " + cse);
            } else {
                log("Error in Sending Dragon Death Notif to WhatsApp, cause int: " + cse);
            }
            if (wsc.status == 0) {
                if (src != null && src.isPlayer()) {
                    const pl = src.toPlayer();
                    wsc.send(JSON.stringify({
                        type: 'notif',
                        message: mobName + ' telah dibunuh oleh ' + pl.realName + '!',
                        name: mobName + ' Death'
                    }));
                } else {
                    wsc.send(JSON.stringify({
                        type: 'notif',
                        message: mobName + ' berhasil dikalahkan!',
                        name: mobName + ' Death'
                    }));
                }
                log("Dragon Death Notif has been sent to Discord, cause int: " + cse);
            } else {
                log("Error in Sending Dragon Death Notif to Discord, cause int: " + cse);
            }
        } catch (error) {
            log("Error in Sending Dragon Death Notif to WhatsApp/Discord: " + error + ", cause int: " + cse);
        }
    }
});

/* mc.listen(`onJoin`, (pl) => {
    try {
        if (wss && wss.clients && waConn == 1) {
            wss.clients.forEach(client => {
                client.send(pl.realName + " joined");
            });
            log("Player Join Notif has been sent to WhatsApp, player: " + pl.realName);
        } else {
            log("Error in Sending Player Join Notif to WhatsApp, player: " + pl.realName);
        }
    } catch (error) {
        log("Error in Sending Player Join Notif to WhatsApp: " + error + ", player: " + pl.realName);
    }
});
 */
/* mc.listen(`onLeft`, (pl) => {
    try {
        if (wss && wss.clients && waConn == 1) {
            wss.clients.forEach(client => {
                client.send(pl.realName + " left");
            });
            log("Player Join Notif has been sent to WhatsApp, player: " + pl.realName);
        } else {
            log("Error in Sending Player Leave Notif to WhatsApp, player: " + pl.realName);
        }
    } catch (error) {
        log("Error in Sending Player Leave Notif to WhatsApp: " + error + ", player: " + pl.realName);
    }
}); */

mc.listen(`onTakeItem`, (pl, en, it) => {
    if (it.type == "minecraft:dragon_egg" && pl.pos.dimid == 2) {
        try {
            if (wss && wss.clients && waConn == 1) {
                wss.clients.forEach(client => {
                    client.send("*Dragon Egg* diambil oleh *" + pl.realName + "*!");
                });
                log("Dragon Egg Taken Notif has been sent to WhatsApp, taker: " + pl.realName);
            } else {
                log("Error in Sending Dragon Egg Taken Notif to WhatsApp, taker: " + pl.realName);
            }
            if (wsc.status == 0) {
                wsc.send(JSON.stringify({
                    type: 'notif',
                    message: 'Dragon Egg diambil oleh ' + pl.realName + '!',
                    name: 'Dragon Egg Taken'
                }));
                log("Dragon Egg Taken Notif has been sent to Discord, taker: " + pl.realName);
            } else {
                log("Error in Sending Dragon Egg Taken Notif to Discord, taker: " + pl.realName);
            }
        } catch (error) {
            log("Error in Sending Dragon Egg Taken Notif to WhatsApp/Discord: " + error + ", taker: " + pl.realName);
        }
    } else if (it.type == "minecraft:elytra" && pl.pos.dimid == 2 && it.lore.length == 0) {
        it.setLore([`Obtained by: ${pl.realName}`]);
        try {
            if (wss && wss.clients && waConn == 1) {
                wss.clients.forEach(client => {
                    client.send("*" + pl.realName + "* mendapatkan *Elytra*!");
                });
                log("Elytra Obtained Notif has been sent to WhatsApp, taker: " + pl.realName);
            } else {
                log("Error in Sending Elytra Obtained Notif to WhatsApp, taker: " + pl.realName);
            }
            if (wsc.status == 0) {
                wsc.send(JSON.stringify({
                    type: 'notif',
                    message: pl.realName + ' mendapatkan Elytra!',
                    name: 'Elytra Obtained'
                }));
                log("Elytra Obtained Notif has been sent to Discord, taker: " + pl.realName);
            } else {
                log("Error in Sending Elytra Obtained Notif to Discord, taker: " + pl.realName);
            }
        } catch (error) {
            log("Error in Sending Elytra Obtained Notif to WhatsApp/Discord: " + error + ", taker: " + pl.realName);
        }
    }
});

// ====================================================================
// EXPORT API UNTUK PLUGIN LAIN (PVP, DLL)
// ====================================================================

// Fungsi Global untuk mengirim notifikasi ke WA & Discord
function sendGlobalNotification(title, message) {
    const formattedMsg = `*${title}*\n${message}`;

    // 1. Kirim ke WhatsApp
    if (wss && wss.clients && waConn == 1) {
        wss.clients.forEach(client => {
            client.send(formattedMsg);
        });
        log(`Notification '${title}' sent to WhatsApp`);
    }

    // 2. Kirim ke Discord
    if (wsc.status === 0) {
        wsc.send(JSON.stringify({
            type: 'notif',
            message: message, // Di Discord embed biasanya ada title sendiri
            name: title
        }));
        log(`Notification '${title}' sent to Discord`);
    }
}

// Daftarkan fungsi ini agar bisa dipanggil plugin lain
ll.exports(sendGlobalNotification, 'WA_Bridge', 'sendNotification');

/* let pvpResult = {
    player1: "",
    player2: "",
    skor1: 0,
    skor2: 0,
    reward: "",
};

mc.listen("onScoreChanged", function (pl, num, name, displayName) {
    if (name === "playerpvp") {
        if (num === 1) {
            pvpResult.player1 = pl.realName;
        } else if (num === 2) {
            pvpResult.player2 = pl.realName;
        }
    } else if (name === "poin") {
        if (pl.name === pvpResult.player1) {
            pvpResult.skor1 = num;
        } else if (pl.name === pvpResult.player2) {
            pvpResult.skor2 = num;
        }
    } else if (name === "rreward") {
        if (num >= 1 && num <= 40) {
            pvpResult.reward = "8 Golden Carrots (Common)"
        } else if (num >= 41 && num <= 70) {
            pvpResult.reward = "16 Experience Bottles (Uncommon)"
        } else if (num >= 71 && num <= 86) {
            pvpResult.reward = "Trident (Rare)"
        } else if (num >= 87 && num <= 96) {
            pvpResult.reward = "Enchanted Golden Apple (Epic)"
        } else if (num >= 97 && num <= 100) {
            pvpResult.reward = "Diamond Block (Legendary) (insane luck)"
        }
    }else if (name === "pvpdone") {
        if (pvpResult.skor1 >= 3 || pvpResult.skor2 >= 3) {
            const winner = pvpResult.skor1 >= 3 ? pvpResult.player1 : pvpResult.player2;
            let winnerPvpPoint = mc.getPlayer(winner).getScore('PVP Point');
            if (pvpResult.reward === "") {
                pvpResult.reward = "_Cooldown_";
            }
            //
            * const loser = pvpResult.skor1 >= 3 ? pvpResult.player2 : pvpResult.player1;
            const winnerScore = Math.max(pvpResult.skor1, pvpResult.skor2);
            const loserScore = Math.min(pvpResult.skor1, pvpResult.skor2); *
            const notification = `*PVP Arena Battle Result*
- ${pvpResult.player1}: ${pvpResult.skor1}
- ${pvpResult.player2}: ${pvpResult.skor2}
Winner: ${winner}!
Reward: ${pvpResult.reward}
${winner}'s PVP Point: ${winnerPvpPoint}`;

            // Send to WhatsApp
            if (wss && wss.clients && waConn == 1) {
                wss.clients.forEach(client => {
                    client.send(notification);
                });
                log("PVP result sent to WhatsApp");
            } else {
                log("Failed to send PVP result to WhatsApp");
            }

            // Send to Discord
            if (wsc.status === 0) {
                wsc.send(
                    JSON.stringify({
                        type: "notif",
                        message: notification,
                        name: "PVP Arena Battle Result",
                    })
                );
                log("PVP result sent to Discord");
            } else {
                log("Failed to send PVP result to Discord");
            }
        } else {
            log("PVP match ended with no winner");
        }

        // Reset results for next match
        pvpResult = {
            player1: "",
            player2: "",
            skor1: 0,
            skor2: 0,
            reward: "",
        };
    }
});
 */

