const fs = require("fs");
// Impor modul 'path' untuk menangani lokasi file dengan benar
const path = require("path");

// Tentukan path file cache secara absolut agar tidak salah lokasi
const cacheFilePath = path.join(__dirname, 'group_cache.json');

let groupMemberCache = {};
// Memuat cache dari file saat bot pertama kali start
if (fs.existsSync(cacheFilePath)) {
	try {
		const data = fs.readFileSync(cacheFilePath, 'utf8');
		if (data) {
			groupMemberCache = JSON.parse(data);
		}
	} catch (e) {
		console.error("Gagal memuat atau parse group_cache.json", e);
		groupMemberCache = {};
	}
}

module.exports = async (conn, msg) => {
	try {
		const groupId = msg.id;
		const participantIds = msg.participants; // Berisi ID dari event (bisa LID atau JID)
		const action = msg.action;

		const groupData = await conn.groupMetadata(groupId);
		
		if (!groupMemberCache[groupId]) {
			groupMemberCache[groupId] = {};
		}

		// =================================================================
		// PERUBAHAN 1: Logika Update Cache Disesuaikan
		// =================================================================
		// Mengisi cache dengan memetakan ID utama (p.id) ke nomor asli (p.realId).
		for (const p of groupData.participants) {
			// p.id bisa berupa LID atau JID, p.realId selalu nomor asli.
			if (p.id && p.realId) {
				groupMemberCache[groupId][p.id] = p.realId;
			}
		}
		
		// Simpan cache ke file setiap kali ada update
		fs.writeFileSync(cacheFilePath, JSON.stringify(groupMemberCache, null, 2));

		const dataLeft = db.cekDatabase("left", "id", groupId) || { id: "", teks: "" };
		const dataWelcome = db.cekDatabase("welcome", "id", groupId) || { id: "", teks: "" };

		if (action === "add") {
			global.statParticipant = true;
		}

		for (const pId of participantIds) {
			let realJid;

			// =================================================================
			// PERUBAHAN 2: Logika Pencarian JID Disesuaikan
			// =================================================================
			if (action === 'add') {
				// Cari anggota baru di metadata, lalu ambil properti 'realId'
				const memberInfo = groupData.participants.find(p => p.id === pId);
				if (memberInfo) {
					realJid = memberInfo.realId;
				}
			} else if (action === 'remove') {
				// Untuk anggota yang keluar, cari di cache seperti sebelumnya
				realJid = groupMemberCache[groupId]?.[pId];
			}
			
			if (!realJid) {
				console.log(`[WelcomeHandler] Gagal menemukan JID asli untuk ID ${pId} dengan aksi "${action}". Ini normal setelah restart.`);
				continue;
			}

			let messageText;

			if (action === "add" && dataWelcome.id.includes(groupId)) {
				messageText = dataWelcome.teks;
			} else if (action === "remove" && dataLeft.id.includes(groupId)) {
				messageText = dataLeft.teks;
			}

			if (typeof messageText !== 'string' || messageText.trim() === '') {
				continue;
			}

			let ppimg;
			try {
				ppimg = await conn.profilePictureUrl(realJid, "image");
			} catch {
				ppimg = config.thumb;
			}

			const userTag = `@${realJid.split("@")[0]}`;
			
			const finalText = messageText
				.replace(/@ownergc/g, `${groupData.owner ? groupData.owner.split("@")[0] : "Tidak Diketahui"}`)
				.replace(/@creation/g, require("moment")(new Date(parseInt(groupData.creation) * 1000)).format("DD MMM YYYY HH:mm:ss"))
				.replace(/@user/g, userTag)
				.replace(/@desc/g, groupData.desc ? groupData.desc.toString() : "tidak ada deskripsi")
				.replace(/@subject/g, groupData.subject);
			
			const messageOptions = {
				mentions: [realJid]
			};

			if (groupId === "6281350402045-1619941633@g.us" || action === 'remove') {
				messageOptions.text = finalText;
			} else if (action === 'add') {
				messageOptions.image = { url: ppimg };
				messageOptions.caption = finalText;
			}

			await conn.sendMessage(groupId, messageOptions);
		}
	} catch (error) {
		console.error("[WelcomeHandler] Terjadi error:", error);
	}
};