require("./global.js");
require("./lib/Proto");
const { getBinaryNodeChild } = require("baileys");
const Baileys = require("baileys");
const { logger } = Baileys.DEFAULT_CONNECTION_CONFIG;
const { serialize } = require("./lib/serialize");
const fs = require("fs");
const { color, getAdmin, isUrl } = require("./lib");
const cooldown = new Map();
const prefix = "#";
const multi_pref = new RegExp("^[" + "!#%&?/;:,.~-+=".replace(/[|\\{}()[\]^$+*?.\-\^]/g, "\\$&") + "]");
const owner = config.owner;

function printSpam(conn, isGc, sender, groupName) {
	if (isGc) {
		return conn.logger.warn("Detect SPAM", color(sender.split("@")[0], "lime"), "in", color(groupName, "lime"));
	}
	if (!isGc) {
		return conn.logger.warn("Detect SPAM", color(sender.split("@")[0], "lime"));
	}
}

function printLog(isCmd, sender, msg, body, groupName, isGc) {
	if (isCmd && isGc) {
		return console.log(
			color("[ COMMAND GC ]", "aqua"),
			color(sender.split("@")[0], "lime"),
			color(body, "aqua"),
			"in",
			color(groupName, "lime")
		);
	}
	if (isCmd && !isGc) {
		return console.log(color("[ COMMAND PC ]", "aqua"), color(sender.split("@")[0], "lime"), color(body, "aqua"));
	}
}

module.exports = handler = async (m, conn, map) => {
	try {
		if (m.type !== "notify") return;
		let ms = m.messages[0];
		ms.message =
			Object.keys(ms.message)[0] === "ephemeralMessage" ? ms.message.ephemeralMessage.message : ms.message;
		let msg = await serialize(JSON.parse(JSON.stringify(ms)), conn);
		if (msg.from.endsWith("@s.whatsapp.net")) return;
		if (!msg.message) return;

		if (map.isSelf) {
			if (!msg.isSelf) return;
		}

		if (Object.keys(msg.message)[0] == "senderKeyDistributionMessage")
			delete msg.message.senderKeyDistributionMessage;
		if (Object.keys(msg.message)[0] == "messageContextInfo") delete msg.message.messageContextInfo;
		if (msg.key && msg.key.remoteJid === "status@broadcast") return;
		if (
			msg.type === "protocolMessage" ||
			msg.type === "senderKeyDistributionMessage" ||
			!msg.type ||
			msg.type === ""
		)
			return;

		let { body, type } = msg;
		global.dashboard = JSON.parse(fs.readFileSync("./database/dashboard.json"));
		global.customLanguage = JSON.parse(fs.readFileSync("./database/language.json"));
		const { isGroup, sender, from } = msg; // 'sender' di sini bisa berupa @lid

		// =================================================================
		// PERBAIKAN UTAMA: Ambil Metadata dan Status Admin dengan Cara Baru
		// =================================================================
		const groupMetadata = isGroup ? await conn.groupMetadata(from) : "";
		const groupName = isGroup ? groupMetadata.subject : "";
		const isPrivate = msg.from.endsWith("@s.whatsapp.net");

		// 1. Dapatkan info peserta untuk pengirim pesan
		const senderParticipant = isGroup ? groupMetadata.participants.find(p => p.id === sender) : undefined;
		// 2. Dapatkan nomor asli pengirim pesan (jika ada) dari properti 'realId'
		const realSenderJid = isGroup && senderParticipant ? senderParticipant.realId : sender;

		// 3. Cek apakah pengirim adalah admin
		const isAdmin = isGroup ? !!senderParticipant?.admin : false;

		// 4. Dapatkan info peserta untuk BOT
		const botJid = conn.decodeJid(conn.user.id);
		const botParticipant = isGroup ? groupMetadata.participants.find(p => p.realId === botJid) : undefined;
		// 5. Cek apakah BOT adalah admin
		const botAdmin = isGroup ? !!botParticipant?.admin : false;
		
		// 6. Cek kepemilikan menggunakan nomor asli pengirim
		const isOwner = owner.includes(realSenderJid);
		// =================================================================
		// AKHIR PERBAIKAN
		// =================================================================

		let temp_pref = multi_pref.test(body) ? body.split("").shift() : "#";
		if (body) {
			body = body.startsWith(temp_pref) ? body : "";
		} else {
			body = "";
		}

		const arg = body.substring(body.indexOf(" ") + 1);
		const args = body.trim().split(/ +/).slice(1);
		const comand = body.trim().split(/ +/)[0];
		let q = body.trim().split(/ +/).slice(1).join(" ");
		const isCmd = body.startsWith(temp_pref);

		//type message
		const isVideo = type === "videoMessage";
		const isImage = type === "imageMessage";
		const isLocation = type === "locationMessage";
		const contentQ = msg.quoted ? JSON.stringify(msg.quoted) : [];
		const isQAudio = type === "extendedTextMessage" && contentQ.includes("audioMessage");
		const isQVideo = type === "extendedTextMessage" && contentQ.includes("videoMessage");
		const isQImage = type === "extendedTextMessage" && contentQ.includes("imageMessage");
		const isQDocument = type === "extendedTextMessage" && contentQ.includes("documentMessage");
		const isQSticker = type === "extendedTextMessage" && contentQ.includes("stickerMessage");
		const isQLocation = type === "extendedTextMessage" && contentQ.includes("locationMessage");

		const Media = (media = {}) => {
			list = [];
			if (media.isQAudio) list.push("audioMessage");
			if (media.isQVideo) list.push("videoMessage");
			if (media.isQImage) list.push("imageMessage");
			if (media.isQDocument) list.push("documentMessage");
			if (media.isQSticker) list.push("stickerMessage");
			return list;
		};

		require("./res/EmitEvent.js")(msg, conn);

		conn.sendMessage = async (jid, content, options = { isTranslate: true }) => {
			await conn.presenceSubscribe(jid);
			const typeMes =
				content.image || content.text || content.video || content.document ? "composing" : "recording";
			await conn.sendPresenceUpdate(typeMes, jid);
			const cotent = content.caption || content.text || "";
			if (options.isTranslate) {
				const footer = content.footer || false;
				const customLang = customLanguage.find((x) => x.jid == msg.sender);
				const language = customLang ? customLang.country : false;
				if (customLang) {
					if (footer) footer = await rzky.tools.translate(footer, language);
					translate = await rzky.tools.translate(cotent, language);
					if (content.video || content.image) {
						content.caption = translate || cotent;
					} else {
						content.text = translate || cotent;
					}
				}
			}
			content.withTag
				? (content.mentions = [...cotent.matchAll(/@([0-9]{5,16}|0)/g)].map((v) => v[1] + "@s.whatsapp.net"))
				: "";
			options.adReply
				? (content.contextInfo = {
						externalAdReply: {
							title: "© " + config.namebot,
							mediaType: 1,
							showAdAttribution: true,
							body:
								config.namebot +
								" multi-device whatsapp bot using JavaScript and made by " +
								config.ownername,
							thumbnail: await conn.getBuffer(config.thumb),
							sourceUrl: "https://github.com/Rizky878/rzky-multidevice/",
						},
				  })
				: "";
			if (
				typeof content === "object" &&
				"disappearingMessagesInChat" in content &&
				typeof content["disappearingMessagesInChat"] !== "undefined" &&
				Baileys.isJidGroup(jid)
			) {
				const { disappearingMessagesInChat } = content;
				const value =
					typeof disappearingMessagesInChat === "boolean"
						? disappearingMessagesInChat
							? Baileys.WA_DEFAULT_EPHEMERAL
							: 0
						: disappearingMessagesInChat;
				await conn.groupToggleEphemeral(jid, value);
			} else {
				const isDeleteMsg = "delete" in content && !!content.delete;
				const additionalAttributes = {};
				if (isDeleteMsg) {
					additionalAttributes.edit = "7";
				}
				const contentMsg = await Baileys.generateWAMessageContent(content, {
					logger,
					userJid: conn.user.id,
					upload: conn.waUploadToServer,
					...options,
				});
				options.userJid = conn.user.id;
				const fromContent = await Baileys.generateWAMessageFromContent(jid, contentMsg, options);
				fromContent.key.id = "RZKY" + require("crypto").randomBytes(13).toString("hex").toUpperCase();
				await conn.relayMessage(jid, fromContent.message, {
					messageId: fromContent.key.id,
					additionalAttributes,
					userJid: conn.user.id,
				});
				process.nextTick(() => {
					conn.ev.emit("messages.upsert", {
						messages: [fromContent],
						type: "append",
					});
				});
				await conn.sendPresenceUpdate("paused", jid);
				return fromContent;
			}
		};

		await conn.readMessages([msg.key]);

		if (!isGroup && require("awesome-phonenumber")("+" + msg.sender.split("@")[0]).getCountryCode() == "212") {
			await conn.sendMessage(msg.from, { text: "Sorry i block you, Please read my whatsapp bio" });
			await require("delay")(3000);
			await conn.updateBlockStatus(msg.sender, "block");
			await conn.sendMessage(config.owner[0], {
				text: "*• Blocked Detected Number +212*\n\nwa.me/" + msg.sender.split("@")[0],
			});
		}
		if (require("awesome-phonenumber")("+" + msg.sender.split("@")[0]).getCountryCode() == "212") return;

		if (isGroup) {
			await require("./lib/antilink")(msg, conn);
		}

		printLog(isCmd, sender, msg, body, groupName, isGroup);

		const cmdName = body.slice(temp_pref.length).trim().split(/ +/).shift().toLowerCase();
		if (cmdName == "cekserver" && msg.key && msg.key.remoteJid == "120363044012116361@g.us") {
			fs.readFile('C:/Users/Administrator/Desktop/OnlineListBuild.json', 'utf8', async (err, data) => {
				if (err) {
				  console.error('Gagal membaca file OnlineList:', err);
				  return;
				}
				const onlineListData = JSON.parse(data);
				let playersText = "Tidak ada player yang online saat ini";
				playersText = onlineListData.map((player, index) => `${index + 1}. ${player}`).join('\n');
				
				await msg.reply(`*Player Online di Server Build:*\n${playersText}`);
				return;
			});
			return;
		}
		
		const cmd =
			map.command.get(msg.body.trim().split(/ +/).shift().toLowerCase()) ||
			[...map.command.values()].find((x) =>
				x.alias.find((x) => x.toLowerCase() == msg.body.trim().split(/ +/).shift().toLowerCase())
			) ||
			map.command.get(cmdName) ||
			[...map.command.values()].find((x) => x.alias.find((x) => x.toLowerCase() == cmdName));
			
		if (isCmd && !cmd) {
			if (msg.key && msg.key.remoteJid == "6281350402045-1619941633@g.us" && !isAdmin) {
				return;
			}
			var data = [...map.command.keys()];
			[...map.command.values()]
				.map((x) => x.alias)
				.join(" ")
				.replace(/ +/gi, ",")
				.split(",")
				.map((a) => data.push(a));
			var result = rzky.tools.detectTypo(cmdName, data);
			if (result.status != 200) return;
			let teks = `Mungkin ini yang Anda maksud?\n\n`;
			let angka = 1;
			if (typeof result.result == "object" && typeof result.result != "undefined") {
				for (let i of result.result) {
					var alias =
						[...map.command.values()].find((x) => x.name == i.teks) ||
						[...map.command.values()].find((x) => x.alias.find((x) => x.toLowerCase() == i.teks));
					teks += `*${angka++}. ${map.prefix}${i.teks}*\n`;
					teks += `Alias: *${alias.alias.join(", ")}*\n`;
					teks += `Akurasi: *${i.keakuratan}*\n\n`;
				}
				teks += `Jika benar, silakan ketik ulang perintah!`;
				await msg.reply(teks);
			}
		}

		if (!cmd) return;
		if (!cooldown.has(from)) {
			cooldown.set(from, new Map());
		}
		const now = Date.now();
		const timestamps = cooldown.get(from);
		const cdAmount = (cmd.options.cooldown || 3) * 60000;
		if (timestamps.has(from)) {
			const expiration = timestamps.get(from) + cdAmount;
			if (now < expiration) {
				let timeLeft = (expiration - now) / 1000;
				printSpam(conn, isGroup, sender, groupName);
				return await conn.sendMessage(
					from,
					{ text: `Grup ini sedang dalam cooldown, silakan tunggu _${timeLeft.toFixed(1)} detik_ lagi` },
					{ quoted: msg }
				);
			}
		}

		setTimeout(() => timestamps.delete(from), cdAmount);
		let optionsCmd = cmd.options;
		
        if (msg.key && msg.key.remoteJid == "6281350402045-1619941633@g.us" && !isAdmin) {
            return false;
        }
		if (optionsCmd.noPrefix) {
			if (isCmd) return;
			q = msg.body.split(" ").splice(1).join(" ");
		} else if (!optionsCmd.noPrefix) {
			if (!isCmd) return;
		}
		if (optionsCmd.isSpam) {
			timestamps.set(from, now);
		}
		if (cmd && cmd.category != "private") {
			let comand = dashboard.find((command) => command.name == cmd.name);
			if (comand) {
				comand.success += 1;
				comand.lastUpdate = Date.now();
				fs.writeFileSync("./database/dashboard.json", JSON.stringify(dashboard));
			} else {
				await db.modified("dashboard", { name: cmd.name, success: 1, failed: 0, lastUpdate: Date.now() });
			}
		}
		if (optionsCmd.isPremium && !isPremium) {
			return await conn.sendMessage(msg.from, { text: response.OnlyPrem }, { quoted: msg });
		}
		if (map.lockcmd.has(cmdName)) {
			let alasan = map.lockcmd.get(cmdName);
			return msg.reply(
				`Maaf bro "${conn.getName(sender)}", perintah "${cmdName}" telah dinonaktifkan oleh Owner\nAlasan: *${alasan || "-"}*`
			);
		}
		if (optionsCmd.isAdmin && !isAdmin) {
			return await conn.sendMessage(msg.from, { text: response.GrupAdmin }, { quoted: msg });
		}
		if (optionsCmd.isQuoted && !msg.quoted) {
			return await msg.reply(`Silakan reply pesan`);
		}
		if (optionsCmd.isMedia) {
			let medianya = Media(optionsCmd.isMedia ? optionsCmd.isMedia : {});
			if (typeof medianya[0] != "undefined" && !medianya.includes(msg.quoted ? msg.quoted.mtype : []))
				return msg.reply(
					`Silakan reply *${medianya
						.map((a) => `${((aa = a.charAt(0).toUpperCase()), aa + a.slice(1).replace(/message/gi, ""))}`)
						.join("/")}*`
				);
		}
		if (optionsCmd.isOwner && !isOwner && !msg.isSelf) {
			return await conn.sendMessage(msg.from, { text: response.OnlyOwner }, { quoted: msg });
		}
		if (optionsCmd.isGroup && !isGroup) {
			return await conn.sendMessage(msg.from, { text: response.OnlyGrup }, { quoted: msg });
		}
		if (optionsCmd.isBotAdmin && !botAdmin) {
			return await conn.sendMessage(msg.from, { text: response.BotAdmin }, { quoted: msg });
		}
		if (optionsCmd.query && !q) {
			return await msg.reply(
				typeof optionsCmd.query == "boolean" && optionsCmd.query ? `Masukan query` : optionsCmd.query
			);
		}
		if (optionsCmd.isPrivate && !isPrivate) {
			return await conn.sendMessage(msg.from, { text: response.OnlyPM }, { quoted: msg });
		}
		if (optionsCmd.isUrl && !isUrl(q ? q : "p")) {
			return await conn.sendMessage(msg.from, { text: response.error.Iv }, { quoted: msg });
		}
		if (optionsCmd.wait) {
			await conn.sendMessage(
				msg.from,
				{ text: typeof optionsCmd.wait == "string" ? optionsCmd.wait : response.wait },
				{ quoted: msg }
			);
		}
		try {
			await cmd.run(
				{ msg, conn },
				{ owner: isOwner, q, map, args, arg, Baileys, prefix: temp_pref, response, chat: m, command: comand }
			);
		} catch (e) {
			if (cmd.category != "private") {
				let fail = dashboard.find((command) => command.name == cmd.name);
				fail.failed += 1;
				fail.success -= 1;
				fail.lastUpdate = Date.now();
				fs.writeFileSync("./database/dashboard.json", JSON.stringify(dashboard));
			}
			await msg.reply(require("util").format(e), { isTranslate: false });
		}
	} catch (e) {
		console.log(color("Error", "red"), e.stack);
	}
};