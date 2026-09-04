/**
 * services/orderService.js
 *
 * Service layer murni buat order/payment — nggak tahu soal command parsing,
 * nggak import `m` atau apapun dari Baileys handler langsung.
 * Bisa dipanggil dari 2 tempat: command handler lama (plugins/buy.js) dan
 * AI tool executor (services/aiOrchestrator.js).
 */

import { Casaku } from "casaku";
import QRCode from "qrcode";
import fs from "fs/promises";
import path from "path";
import { createCanvas, loadImage } from "@napi-rs/canvas";

// ====== KONFIGURASI ======
const casaku = new Casaku({
    licenseKey: process.env.CASAKU_LICENSE_KEY || "cashify_a236950d9c2edcc2db4d18d7f593320e6d37fa2ebd6a85959b5cd488a0913ef0",
    qrId: process.env.CASAKU_QR_ID || "8f416161-b590-407a-aefc-17b4d2fb3666",
});

const FIREBASE_URL =
    process.env.FIREBASE_URL ||
    "https://eujianauth-default-rtdb.asia-southeast1.firebasedatabase.app";

// ====== FRAME QR (canvas) ======
const QR_FRAME_PATH = path.join(process.cwd(), "assets", "qr-frame.png");
const FRAME_SIZE = 1000;
const QR_SIZE = 450;
const QR_OFFSET = Math.round((FRAME_SIZE - QR_SIZE) / 2);
const QR_OFFSETY = QR_OFFSET + 35;

// Cache frame biar cuma di-load sekali dari disk
let framePromise = null;
function getFrameImage() {
    if (!framePromise) {
        framePromise = fs.readFile(QR_FRAME_PATH).then(buf => loadImage(buf));
    }
    return framePromise;
}

// QR di bawah, frame transparan di atas
async function generateFramedQR(qrString) {
    let frameImg = null;
    try {
        frameImg = await getFrameImage();
    } catch (err) {
        console.warn("[orderService] Frame image gagal diload (mungkin tidak ada). Mengirim QR tanpa frame.");
    }

    const qrBuffer = await QRCode.toBuffer(qrString, { width: QR_SIZE, margin: 1 });
    if (!frameImg) {
        return qrBuffer; // Fallback ke QR polos
    }

    const qrImg = await loadImage(qrBuffer);

    const canvas = createCanvas(FRAME_SIZE, FRAME_SIZE);
    const ctx = canvas.getContext("2d");

    ctx.fillStyle = "#FFFFFF";
    ctx.fillRect(0, 0, FRAME_SIZE, FRAME_SIZE);
    ctx.drawImage(qrImg, QR_OFFSET, QR_OFFSETY, QR_SIZE, QR_SIZE);
    ctx.drawImage(frameImg, 0, 0, FRAME_SIZE, FRAME_SIZE);

    return canvas.toBuffer("image/png");
}

// ====== PRODUK ======
export const PRODUCTS = [
    // --- Produk E-Ujian ---
    { alias: "buy7", label: "akses e-ujian 7 hari", price: 15000, duration: 7, fulfillment: "license_key", description: "Paket 7 Hari (E-Ujian)" },
    { alias: "buy10", label: "akses e-ujian 10 hari", price: 25000, duration: 10, fulfillment: "license_key", bestSeller: true, description: "Paket 10 Hari (E-Ujian, Best Seller)" },
    { alias: "buy14", label: "akses e-ujian 14 hari", price: 30000, duration: 14, fulfillment: "license_key", description: "Paket 14 Hari (E-Ujian)" },
    { alias: "buy30", label: "akses e-ujian 30 hari", price: 35000, duration: 30, fulfillment: "license_key", description: "Paket 30 Hari (E-Ujian)" },

    // --- Produk MyExams ---
    { alias: "buy7my", label: "akses myexams 7 hari", price: 15000, duration: 7, fulfillment: "license_key", description: "Paket 7 Hari (MyExams)" },
    { alias: "buy10my", label: "akses myexams 10 hari", price: 25000, duration: 10, fulfillment: "license_key", description: "Paket 10 Hari (MyExams)" },
    { alias: "buy14my", label: "akses myexams 14 hari", price: 30000, duration: 14, fulfillment: "license_key", description: "Paket 14 Hari (MyExams)" },
    { alias: "buy30my", label: "akses myexams 30 hari", price: 35000, duration: 30, fulfillment: "license_key", description: "Paket 30 Hari (MyExams)" },

    // --- Produk Lainnya ---
    { alias: "buyseb", label: "Akun SEB patch", price: 55000, fulfillment: "external_account", description: "Akun permanen tanpa expiry" },
];

// Cegah spam generate QR saat transaksi masih pending
export const pendingWatch = new Set();

// Simpan message key pesan QR per paymentId (in-memory)
export const qrMessageKeys = new Map();

// ====== BRAND ======
export const BRAND_FOOTER = "© Lynix Store";

// ====== HELPER TEKS ======
export function box(title, lines = []) {
    let t = `╭╮ \`✦ ${title}\`\n`;
    for (const l of lines) t += `││  ${l}\n`;
    t += `╰╯`;
    return t;
}

export function buildProductListFallback(prefix) {
    return PRODUCTS.map(
        (p) =>
            `${prefix}${p.alias} - ${p.label} - Rp${p.price.toLocaleString("id-ID")}${p.bestSeller ? " ⭐ BEST SELLER" : ""
            }`
    ).join("\n");
}

// ====== FIREBASE: generate license key ======
function formatExpiryDate(durationDays) {
    const d = new Date();
    d.setDate(d.getDate() + durationDays);
    const day = String(d.getDate()).padStart(2, "0");
    const month = String(d.getMonth() + 1).padStart(2, "0");
    const year = d.getFullYear();
    return `${day}/${month}/${year}`;
}

function randomKeySuffix() {
    return Math.random().toString(36).substring(2, 8).toUpperCase();
}

export async function createLicenseKey(durationDays, targetDbUrl = FIREBASE_URL) {
    const expiry = formatExpiryDate(durationDays);
    let keyId;

    for (let attempt = 0; attempt < 5; attempt++) {
        keyId = "LYNX_" + randomKeySuffix();
        const checkRes = await fetch(`${targetDbUrl}/keys/${keyId}.json`);
        const existing = await checkRes.json();
        if (!existing) break;
    }

    const putRes = await fetch(`${targetDbUrl}/keys/${keyId}.json`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ expiry, hwid: "" }),
    });

    if (!putRes.ok) throw new Error(`Firebase PUT gagal (status ${putRes.status})`);

    return { keyId, expiry };
}

// ====== EXTERNAL ACCOUNT FULFILLMENT ======
let adminTokenCache = null;
let adminTokenExpiry = 0;
let loginInFlight = null;

async function getAdminToken() {
    if (adminTokenCache && Date.now() < adminTokenExpiry) {
        return adminTokenCache;
    }
    if (loginInFlight) return loginInFlight;

    loginInFlight = (async () => {
        try {
            const baseUrl = process.env.ACCOUNT_API_BASE;
            if (!baseUrl) throw new Error("ACCOUNT_API_BASE belum disetting.");

            const res = await fetch(`${baseUrl}/admin/login`, {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({
                    username: process.env.ACCOUNT_API_ADMIN_USER,
                    password: process.env.ACCOUNT_API_ADMIN_PASS
                })
            });

            const data = await res.json();
            if (!res.ok || !data.success) {
                throw new Error(data.message || data.error || "Gagal login admin");
            }

            adminTokenCache = data.token;
            // Buffer expiry sedikit (mis. 23 jam) untuk amannya. (23 * 60 * 60 * 1000 = 82800000)
            adminTokenExpiry = Date.now() + 82800000;
            return adminTokenCache;
        } finally {
            loginInFlight = null;
        }
    })();

    return loginInFlight;
}

function randomPassword() {
    return Math.random().toString(36).substring(2, 6);
}

export async function generateExternalAccount() {
    const baseUrl = process.env.ACCOUNT_API_BASE;
    if (!baseUrl) throw new Error("ACCOUNT_API_BASE belum disetting.");

    let token = await getAdminToken();

    for (let attempt = 0; attempt < 3; attempt++) {
        const username = "LX_" + Math.random().toString(36).substring(2, 7).toUpperCase();
        const password = randomPassword();

        let res = await fetch(`${baseUrl}/addusr`, {
            method: "POST",
            headers: {
                "Content-Type": "application/json",
                "Authorization": `Bearer ${token}`
            },
            body: JSON.stringify({ username, password })
        });

        if (res.status === 401 && attempt === 0) {
            // Token expired/invalid, reset cache & login ulang
            adminTokenCache = null;
            adminTokenExpiry = 0;
            token = await getAdminToken();
            attempt--; // Coba ulangi iterasi ini
            continue;
        }

        const data = await res.json();

        if (res.status === 400 && data.error === "username already exists") {
            continue; // Generate username baru
        }

        if (!res.ok || !data.success) {
            throw new Error(data.message || data.error || "Gagal membuat akun eksternal");
        }

        return { username, password };
    }

    throw new Error("Gagal generate akun unik setelah beberapa kali percobaan.");
}

// ====== LOCAL TRANSACTION STORE ======
const TRANSACTIONS_FILE = path.join(process.cwd(), "database", "transactions.json");
let writeQueue = Promise.resolve();

export async function readTransactions() {
    try {
        const raw = await fs.readFile(TRANSACTIONS_FILE, "utf-8");
        return JSON.parse(raw);
    } catch (err) {
        if (err.code === "ENOENT") return {};
        throw err;
    }
}

async function writeTransactions(data) {
    await fs.mkdir(path.dirname(TRANSACTIONS_FILE), { recursive: true });
    await fs.writeFile(TRANSACTIONS_FILE, JSON.stringify(data, null, 2));
}

export function saveTransaction(record) {
    writeQueue = writeQueue.then(async () => {
        const data = await readTransactions();
        data[record.paymentId] = record;
        await writeTransactions(data);
    });
    return writeQueue;
}

export function updateTransaction(paymentId, patch) {
    writeQueue = writeQueue.then(async () => {
        const data = await readTransactions();
        if (data[paymentId]) {
            Object.assign(data[paymentId], patch, { updatedAt: new Date().toISOString() });
            await writeTransactions(data);
        }
    });
    return writeQueue;
}

function generatePaymentId() {
    const rand = Math.random().toString(36).substring(2).toUpperCase().padEnd(9, "0").slice(0, 9);
    return `LYNX-APPKEY-${rand}`;
}

// Rakit message key dari return value sock.sendMessage() (string ID di ourin-baileys)
export function buildMessageKey(sentMsg, chat, sock, isGroup) {
    if (!sentMsg) {
        console.warn("[orderService] sock.sendMessage() return undefined/null, nggak bisa ambil key buat delete nanti.");
        return null;
    }

    if (typeof sentMsg === "string") {
        const botJid = sock.user?.id?.split(":")[0] + "@s.whatsapp.net";
        return {
            remoteJid: chat,
            id: sentMsg,
            fromMe: true,
            ...(isGroup ? { participant: botJid } : {}),
        };
    }

    const candidate =
        sentMsg.key ||
        sentMsg.message?.key ||
        (Array.isArray(sentMsg) ? sentMsg[0]?.key : null);

    if (!candidate) {
        console.warn("[orderService] Nggak nemu .key di return value sock.sendMessage(). Struktur:", JSON.stringify(sentMsg, null, 2));
        return null;
    }
    return candidate;
}

function resolveQuoted(quotedMsg) {
    if (!quotedMsg) return undefined;
    const raw = quotedMsg.raw || quotedMsg;
    if (raw && raw.key && raw.message) return raw;
    return undefined;
}

// ==========================================
// === EXPORTED SERVICE FUNCTIONS (murni) ===
// ==========================================

/**
 * Kembalikan array PRODUCTS — buat referensi di AI tool maupun UI.
 */
export function listProducts() {
    return PRODUCTS;
}

/**
 * Kirim pesan interaktif daftar paket ke user.
 * Kalau interactive button gagal, fallback ke teks biasa.
 *
 * @param {{ chat: string, sock: object, quotedMsg: object|null, prefix?: string }} param
 */
export async function showPricelist({ chat, sock, quotedMsg, prefix = "." }) {
    const rawQuoted = resolveQuoted(quotedMsg);
    const opts = rawQuoted ? { quoted: rawQuoted } : {};

    const textList = box("DAFTAR PAKET LYNIX STORE", [
        "Silakan pilih durasi paket kamu:",
        "",
        ...PRODUCTS.map(
            (p) =>
                `• *${prefix}${p.alias}* : ${p.label} - Rp${p.price.toLocaleString("id-ID")}${p.bestSeller ? " ⭐ BEST SELLER" : ""}`
        ),
        "",
        "💡 _Ketik command di atas (contoh: *.buy10*) atau sebutkan durasi ke AI (contoh: *\"mau paket 10 hari\"*)_"
    ]);

    const bestSellers = PRODUCTS.filter((p) => p.bestSeller);
    const regulars = PRODUCTS.filter((p) => !p.bestSeller);

    const toRow = (p) => ({
        header: p.bestSeller ? "🔥 Paket Akses" : "📦 Paket Akses",
        title: `${p.bestSeller ? "⭐ " : ""}${p.label} - Rp${p.price.toLocaleString("id-ID")}`,
        description: p.bestSeller
            ? `⭐ BEST SELLER - Durasi aktif ${p.duration} hari`
            : `Durasi aktif ${p.duration} hari`,
        id: `${p.description}`,
        rowId: `ORDER_${p.alias}`,
    });

    const sections = [];
    if (bestSellers.length > 0) {
        sections.push({
            title: "Paling Laris",
            highlight_label: "⭐ BEST SELLER",
            rows: bestSellers.map(toRow),
        });
    }
    sections.push({
        title: "Paket Lainnya",
        rows: regulars.map(toRow),
    });

    console.log(`[orderService] 📤 Sending pricelist text to ${chat}...`);
    try {
        const res = await sock.sendMessage(chat, {
            text: box("PILIH PAKET", ["Silakan pilih durasi paket kamu di bawah ini 👇"]),
            footer: BRAND_FOOTER,
            buttons: [
                {
                    text: "🛒 Lihat Paket",
                    sections,
                }
            ]
        });
        console.log(`[orderService] ✅ Pricelist SENT to ${chat} (ID: ${res?.key?.id || "OK"})`);
    } catch (err) {
        console.error("[orderService] ❌ Gagal kirim pricelist, fallback ke teks:", err.message);
        await sock.sendMessage(chat, { text: textList });
    }

    // Kembalikan ringkasan hasil buat AI
    return {
        status: "pricelist_shown",
        products: PRODUCTS.map((p) => ({
            alias: p.alias,
            label: p.label,
            price: p.price,
            duration: p.duration,
        })),
    };
}

/**
 * Buat pesanan baru — generate QRIS via Casaku, simpan transaksi, kirim QR ke WA.
 *
 * @param {{ sender: string, chat: string, sock: object, productAlias: string, quotedMsg: object|null, prefix?: string, isGroup?: boolean }} param
 * @returns {{ status, paymentId, product, amount } | { status: "error", reason }}
 */
export async function createOrder({ sender, chat, sock, productAlias, quotedMsg, prefix = ".", isGroup = false }) {
    const product = PRODUCTS.find((p) => p.alias === productAlias);
    if (!product) {
        return { status: "error", reason: `Produk '${productAlias}' tidak ditemukan.` };
    }

    const watchKey = `${sender}:${product.alias}`;
    if (pendingWatch.has(watchKey)) {
        await sock.sendMessage(
            chat,
            { text: "⚠️ Kamu masih punya transaksi pending untuk paket ini. Selesaikan atau tunggu expired dulu ya." },
            quotedMsg ? { quoted: quotedMsg } : {}
        );
        return { status: "already_pending", productAlias, label: product.label };
    }

    let trx;
    try {
        trx = await casaku.generateQRISv2({
            qr_id: process.env.CASAKU_QR_ID || "8f416161-b590-407a-aefc-17b4d2fb3666",
            amount: product.price,
            packageIds: ["id.dana"],
            qrType: "dynamic",
            paymentMethod: "qris",
            useQris: true,
            useUniqueCode: true,
            expiredInMinutes: 15,
        });
    } catch (err) {
        await sock.sendMessage(
            chat,
            { text: `❌ Gagal membuat QR pembayaran: ${err.message}` },
            quotedMsg ? { quoted: quotedMsg } : {}
        );
        return { status: "error", reason: err.message };
    }

    const { transactionId, totalAmount, qr_string, expiredInMinutes } = trx.data;
    const paymentId = generatePaymentId();

    await saveTransaction({
        paymentId,
        transactionId,
        sender,
        productAlias: product.alias,
        productLabel: product.label,
        duration: product.duration,
        amount: totalAmount,
        status: "pending",
        createdAt: new Date().toISOString(),
    });

    const qrBuffer = await generateFramedQR(qr_string);
    const rawQuoted = resolveQuoted(quotedMsg);
    const opts = rawQuoted ? { quoted: rawQuoted } : {};

    const qrMsg = await sock.sendMessage(
        chat,
        {
            image: qrBuffer,
            caption:
                box("PEMBAYARAN", [
                    `Paket   : ${product.label}`,
                    `Total   : Rp${totalAmount.toLocaleString("id-ID")}`,
                    `Payment ID : ${paymentId}`,
                    `Kadaluwarsa : ${expiredInMinutes} menit`,
                ]) + "\n\nScan QR di atas buat bayar. Kalau udah bayar, tunggu konfirmasi otomatis dari bot ya.",
            footer: BRAND_FOOTER,
            buttons: [{
                text: "❌ Cancel Order",
                id: `${prefix}cancelorder`
            }]
        }
    );

    const qrKey = buildMessageKey(qrMsg, chat, sock, isGroup);
    if (qrKey) qrMessageKeys.set(paymentId, qrKey);

    // Polling status pembayaran di background
    pendingWatch.add(watchKey);
    casaku.watchPayment(transactionId, {
        interval: 3000,
        timeout: expiredInMinutes * 60 * 1000,
        onStatusChange: async (status, res) => {
            if (status === "paid" || status === "success") {
                pendingWatch.delete(watchKey);

                // Hapus pesan QR — udah nggak relevan
                const qrKeyToDelete = qrMessageKeys.get(paymentId);
                if (qrKeyToDelete) {
                    try {
                        await sock.sendMessage(chat, { delete: qrKeyToDelete });
                    } catch (err) {
                        if (!err.message?.includes("not found") && !err.message?.includes("forbidden")) {
                            console.error("[orderService] Gagal hapus pesan QR:", err.message);
                        }
                    }
                    qrMessageKeys.delete(paymentId);
                }

                try {
                    let successText = "";
                    let copyText = "";

                    let downloadLink = process.env.LINK_DOWNLOAD_EUJIAN || "https://lynix.my.id/eujian312";
                    if (product.alias.endsWith("my")) {
                        downloadLink = process.env.LINK_DOWNLOAD_MYEXAMS || "https://lynix.my.id/myexams-clone";
                    } else if (product.alias === "buyseb") {
                        downloadLink = process.env.LINK_DOWNLOAD_SEB || "https://lynix.my.id/sebmod";
                    }

                    if (product.fulfillment === "license_key") {
                        let targetDbUrl = FIREBASE_URL;
                        if (product.alias.endsWith("my")) {
                            targetDbUrl = process.env.MYEXAMS_FIREBASE_URL || "https://authhhh-d9ca9-default-rtdb.asia-southeast1.firebasedatabase.app";
                        }

                        const keyResult = await createLicenseKey(product.duration, targetDbUrl);
                        await updateTransaction(paymentId, {
                            status: "paid",
                            keyId: keyResult.keyId,
                            expiry: keyResult.expiry,
                            fulfillmentResult: "license_key"
                        });

                        successText = box("PAYMENT SUCCESS", [
                            `Paket   : ${product.label}`,
                            `Total   : Rp${res.data.amount.toLocaleString("id-ID")}`,
                            `Payment ID : ${paymentId}`,
                            `Key     : ${keyResult.keyId}`,
                            `Berlaku sampai : ${keyResult.expiry}`,
                            `Download : ${downloadLink}`,
                        ]) + "\n\n✅ Pembayaran berhasil, terima kasih!\n\n_Simpan key di atas baik-baik, jangan sampai ke-share ke orang lain._";

                        copyText = keyResult.keyId;
                    } else if (product.fulfillment === "external_account") {
                        const accResult = await generateExternalAccount();
                        await updateTransaction(paymentId, {
                            status: "paid",
                            fulfillmentResult: "external_account",
                            accountUsername: accResult.username
                            // password TIDAK disimpan di transaksi mentah
                        });

                        successText = box("PAYMENT SUCCESS", [
                            `Paket   : ${product.label}`,
                            `Total   : Rp${res.data.amount.toLocaleString("id-ID")}`,
                            `Payment ID : ${paymentId}`,
                            `Username : ${accResult.username}`,
                            `Password : ${accResult.password}`,
                            `Download : ${downloadLink}`,
                        ]) + "\n\n✅ Pembayaran berhasil, terima kasih!\n\n_Simpan akun di atas baik-baik, jangan sampai ke-share ke orang lain._";

                        copyText = `Username: ${accResult.username}\nPassword: ${accResult.password}`;
                    } else {
                        throw new Error(`Fulfillment type '${product.fulfillment}' tidak dikenali.`);
                    }

                    try {
                        await sock.sendMessage(
                            chat,
                            {
                                text: successText,
                                footer: BRAND_FOOTER,
                                nativeFlow: [{
                                    text: '📋 Copy Akses',
                                    copy: copyText
                                }]
                            }
                        );
                    } catch (err) {
                        console.error("[orderService] Gagal kirim successText:", err.message);
                    }
                } catch (err) {
                    console.error("[orderService] Gagal fulfillment order:", err);
                    await updateTransaction(paymentId, { status: "paid_key_failed" });
                    await sock.sendMessage(
                        chat,
                        {
                            text:
                                box("PAYMENT SUCCESS", [
                                    `Paket : ${product.label}`,
                                    `Total : Rp${res.data.amount.toLocaleString("id-ID")}`,
                                    `Payment ID : ${paymentId}`,
                                ]) +
                                "\n\n✅ Pembayaran berhasil, tapi ada kendala saat generate akses otomatis.\n⚠️ Hubungi admin buat ambil akses kamu manual ya (kirim Payment ID di atas).",
                        },
                        // opts removed
                    );
                }
            } else if (status === "expired" || status === "cancel") {
                pendingWatch.delete(watchKey);
                qrMessageKeys.delete(paymentId);
                await updateTransaction(paymentId, { status });
                await sock.sendMessage(
                    chat,
                    {
                        text: `❌ Pembayaran untuk paket ${product.label} ${status === "expired" ? "kadaluwarsa" : "dibatalkan"
                            }.\nPayment ID: ${paymentId}`,
                    },
                    // opts removed
                );
            }
        },
        onError: (err) => {
            pendingWatch.delete(watchKey);
            console.error("[orderService] watchPayment error:", err);
        },
    });

    // Ringkasan hasil buat AI (jangan masukin key mentah di sini)
    return {
        status: "qr_sent",
        paymentId,
        product: product.label,
        amount: totalAmount,
        expiredInMinutes,
    };
}

/**
 * Batalkan transaksi pending milik sender.
 *
 * @param {{ sender: string, chat: string, sock: object, quotedMsg: object|null }} param
 * @returns {{ status, paymentId, label } | { status: "no_pending" }}
 */
export async function cancelPendingOrder({ sender, chat, sock, quotedMsg }) {
    const data = await readTransactions();
    const pending = Object.values(data)
        .filter((t) => t.sender === sender && t.status === "pending")
        .sort((a, b) => new Date(b.createdAt) - new Date(a.createdAt))[0];

    const rawQuoted = resolveQuoted(quotedMsg);
    const opts = rawQuoted ? { quoted: rawQuoted } : {};

    if (!pending) {
        await sock.sendMessage(chat, { text: "Nggak ada transaksi pending buat kamu saat ini." });
        return { status: "no_pending" };
    }

    try {
        await casaku.cancelPayment(pending.transactionId);
    } catch (err) {
        console.error("[orderService] Gagal cancel payment di Casaku:", err.message);
        // tetap lanjut bersihin state lokal
    }

    casaku.stopWatch?.(pending.transactionId);
    pendingWatch.delete(`${pending.sender}:${pending.productAlias}`);
    await updateTransaction(pending.paymentId, { status: "cancelled_by_user" });

    const qrKey = qrMessageKeys.get(pending.paymentId);
    if (qrKey) {
        try {
            await sock.sendMessage(chat, { delete: qrKey });
        } catch (err) {
            if (!err.message?.includes("not found") && !err.message?.includes("forbidden")) {
                console.error("[orderService] Gagal hapus pesan QR:", err.message);
            }
        }
        qrMessageKeys.delete(pending.paymentId);
    }

    await sock.sendMessage(chat, {
        react: { text: "✅", key: quotedMsg?.key || undefined }
    });
    await sock.sendMessage(
        chat,
        { text: `❌ Order ${pending.productLabel} dibatalkan.\nPayment ID: ${pending.paymentId}` },
        // opts removed
    );

    return {
        status: "cancelled",
        paymentId: pending.paymentId,
        label: pending.productLabel,
    };
}

/**
 * Cek transaksi terbaru milik sender.
 *
 * @param {{ sender: string }} param
 * @returns {object|null} transaksi, atau null kalau nggak ada
 */
export async function getLatestOrder({ sender }) {
    const data = await readTransactions();
    const latest = Object.values(data)
        .filter((t) => t.sender === sender)
        .sort((a, b) => new Date(b.createdAt) - new Date(a.createdAt))[0];
    return latest || null;
}