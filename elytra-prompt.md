# Coding Agent Prompt — Elytra Monopoly Tracker Plugin (Paper) + WA Bot Bridge

## Konteks
Plugin ini digunakan untuk melacak **setiap** elytra yang diambil dari End Ship (bukan cuma yang pertama, karena advancement `Sky's the Limit` hanya trigger sekali per player), lalu mengirim notifikasi ke WA bot supaya bisa memantau potensi monopoli elytra.

## Tujuan
1. Deteksi pengambilan elytra dari `ItemFrame` di End Ship secara real-time (setiap kali, bukan cuma sekali per player).
2. Tag setiap elytra dengan metadata (lore + PersistentDataContainer) berisi nama pemilik asal, nomor urut pengambilan, dan ID unik.
3. Simpan counter permanen per player: total elytra yang pernah mereka ambil dari End Ship.
4. Kirim 2 jenis notifikasi ke WhatsApp bot lewat HTTP webhook:
   - Pesan singkat real-time: `"PlayerA mendapatkan elytra ke-3!"`
   - Leaderboard/tabel ringkasan: daftar semua player yang punya elytra beserta jumlahnya, dikirim setelah setiap pickup (atau opsional dijadwalkan tiap interval, buat dikonfigurasi).

## Tech Stack

- Storage: SQLite (pakai library `org.xerial:sqlite-jdbc`) atau flatfile YAML — pilih SQLite untuk reliability
- HTTP client: Java 11+ `java.net.http.HttpClient` (built-in, tidak perlu dependency tambahan) untuk POST ke WA bot API

## Struktur Data

### Tabel `elytra_log` (SQLite)
| kolom | tipe | keterangan |
|---|---|---|
| id | INTEGER PRIMARY KEY AUTOINCREMENT | |
| item_uuid | TEXT | UUID unik yang ditanam di PDC tiap elytra |
| owner_uuid | TEXT | UUID player yang pertama kali ambil |
| owner_name | TEXT | nama player saat pickup (cache) |
| pickup_number | INTEGER | urutan pengambilan elytra ini untuk player tsb (ke-1, ke-2, dst) |
| timestamp | INTEGER | epoch millis |
| world | TEXT | nama world (biasanya `world_the_end`) |
| x, y, z | INTEGER | koordinat item frame |

### Tabel `player_elytra_count` (agregat, atau bisa di-query langsung dari `elytra_log` pakai GROUP BY)
| kolom | tipe |
|---|---|
| owner_uuid | TEXT PRIMARY KEY |
| owner_name | TEXT |
| total_count | INTEGER |

## Detail Implementasi

### 1. Event Listener — Deteksi Pickup dari ItemFrame
Tangkap dua jalur pengambilan elytra dari frame:

- **Klik kanan frame** → `PlayerInteractEntityEvent`
  - Cek `event.getRightClicked() instanceof ItemFrame`
  - Cek `frame.getItem().getType() == Material.ELYTRA`
  - PENTING: event ini fire SEBELUM item benar-benar pindah ke player kalau player masih survival & frame masih ada isinya — validasi di tick berikutnya (`Bukkit.getScheduler().runTask`) bahwa frame sudah kosong (`frame.getItem().getType() == Material.AIR`) untuk konfirmasi pickup berhasil, supaya tidak false-positive kalau interact di-cancel oleh plugin lain.

- **Break/attack frame** → `HangingBreakByEntityEvent` atau `EntityDamageByEntityEvent` dengan `event.getEntity() instanceof ItemFrame`
  - Cek isi frame sebelum break: `((ItemFrame) event.getEntity()).getItem()`
  - Kalau elytra, drop item akan otomatis terjadi vanilla — tag item ini juga.

Gunakan flag/debounce sederhana (map `frameUUID -> timestamp`) untuk menghindari double-trigger kalau kedua event ke-fire hampir bersamaan untuk frame yang sama.

### 2. Tagging Item (Lore + PDC)
Begitu pickup terkonfirmasi:

```java
NamespacedKey keyOwner = new NamespacedKey(plugin, "elytra_owner");
NamespacedKey keyId = new NamespacedKey(plugin, "elytra_uid");
NamespacedKey keyNumber = new NamespacedKey(plugin, "elytra_number");

ItemStack elytra = ...; // item yang diambil
ItemMeta meta = elytra.getItemMeta();

String uid = UUID.randomUUID().toString();
int newCount = db.incrementAndGetCount(player.getUniqueId(), player.getName());

PersistentDataContainer pdc = meta.getPersistentDataContainer();
pdc.set(keyOwner, PersistentDataType.STRING, player.getUniqueId().toString());
pdc.set(keyId, PersistentDataType.STRING, uid);
pdc.set(keyNumber, PersistentDataType.INTEGER, newCount);

List<String> lore = new ArrayList<>();
lore.add(ChatColor.GRAY + "Diambil oleh: " + ChatColor.YELLOW + player.getName());
lore.add(ChatColor.GRAY + "Elytra ke-" + newCount);
meta.setLore(lore);

elytra.setItemMeta(meta);
```

Catatan: PDC tetap bertahan walau lore dihapus lewat anvil, jadi PDC adalah source-of-truth untuk scanning, lore cuma untuk display visual ke player.

### 3. Database Layer
Buat class `ElytraDatabase` dengan method:
- `init()` — buat tabel kalau belum ada, taruh file `.db` di folder data plugin
- `logPickup(UUID itemUuid, UUID ownerUuid, String ownerName, int pickupNumber, Location loc)` — insert ke `elytra_log`
- `incrementAndGetCount(UUID ownerUuid, String ownerName)` — upsert ke `player_elytra_count`, return count terbaru
- `getLeaderboard(int limit)` — SELECT ORDER BY total_count DESC, return `List<LeaderboardEntry>` (name + count)
- Semua query DB dijalankan di **async thread** (`Bukkit.getScheduler().runTaskAsynchronously`) supaya tidak block main thread server.


Payload JSON yang dikirim (sesuaikan struktur ini dengan API bot WA lu yang sudah ada):

**Pesan real-time pickup:**
```json
{
  "target": "120363xxxxx@g.us",
  "message": "⚡ PlayerA mendapatkan elytra ke-3!\n📍 Lokasi: world_the_end (1024, 70, -512)"
}
```

**Pesan leaderboard (kirim terpisah, setelah pesan pickup):**
```json
{
  "target": "120363xxxxx@g.us",
  "message": "🏆 *Leaderboard Elytra*\n1. PlayerA — 3 elytra\n2. PlayerB — 2 elytra\n3. PlayerC — 1 elytra"
}
```

Implementasi HTTP call (async, jangan block main thread):

```java
public void sendMessage(String message) {
    Bukkit.getScheduler().runTaskAsynchronously(plugin, () -> {
        try {
            HttpClient client = HttpClient.newHttpClient();
            String json = new Gson().toJson(Map.of(
                "target", config.getTargetGroupId(),
                "message", message
            ));
            HttpRequest request = HttpRequest.newBuilder()
                .uri(URI.create(config.getWebhookUrl()))
                .header("Content-Type", "application/json")
                .header("Authorization", "Bearer " + config.getApiKey())
                .POST(HttpRequest.BodyPublishers.ofString(json))
                .build();
            HttpResponse<String> response = client.send(request, HttpResponse.BodyHandlers.ofString());
            if (response.statusCode() >= 300) {
                plugin.getLogger().warning("WA bridge gagal kirim notif: " + response.statusCode() + " " + response.body());
            }
        } catch (Exception e) {
            plugin.getLogger().warning("WA bridge error: " + e.getMessage());
        }
    });
}
```

Format leaderboard jadi string:
```java
public String formatLeaderboard(List<LeaderboardEntry> entries) {
    StringBuilder sb = new StringBuilder("🏆 *Leaderboard Elytra*\n");
    int rank = 1;
    for (LeaderboardEntry entry : entries) {
        sb.append(rank++).append(". ").append(entry.name())
          .append(" — ").append(entry.count()).append(" elytra\n");
    }
    return sb.toString();
}
```

### 5. Alur Lengkap (Flow)
1. Player klik kanan / break ItemFrame berisi elytra di End Ship
2. Listener konfirmasi pickup valid → panggil `ElytraDatabase.incrementAndGetCount()` (async)
3. Setelah dapat count terbaru → tag item dengan lore + PDC (sync, di main thread karena modify ItemStack)
4. Panggil `ElytraDatabase.logPickup()` untuk audit trail (async)
5. Kirim WA notif real-time: `"⚡ {player} mendapatkan elytra ke-{count}!"`
6. Kalau `send-leaderboard-every-pickup: true` → langsung ambil leaderboard terbaru & kirim sebagai pesan kedua
7. Kalau `false` → leaderboard dikirim via scheduled task (`BukkitScheduler.runTaskTimerAsynchronously`) sesuai `leaderboard-interval-minutes`

### 6. Command Tambahan (opsional tapi berguna)
- `/elytraboard` — tampilkan leaderboard langsung di chat in-game (in-game version dari WA leaderboard)
- `/elytraboard reload` — reload config.yml (permission: `elytratracker.admin`)
- `/elytracheck <player>` — admin cek total elytra spesifik player

### 7. Edge Cases yang Harus Dihandle
- Player ambil elytra tapi inventory penuh → item drop ke ground, tetap harus ke-tag (karena tagging terjadi sebelum item nyampe inventory)
- Frame kena break oleh **non-player** (misal explosion, piston) → jangan trigger notif (cek `HangingBreakEvent` biasa, bukan `HangingBreakByEntityEvent`, untuk kasus ini skip)
- Server restart saat proses async DB write berlangsung → pastikan `onDisable()` menutup koneksi SQLite dengan aman, tidak ada tulisan yang corrupt
- Elytra yang sudah pernah di-tag lalu di-drop/dibuang dan diambil ulang oleh player lain → JANGAN re-tag atau re-count (cek dulu PDC-nya sudah ada `elytra_uid` atau belum, kalau sudah ada berarti ini bukan pickup baru dari frame, cukup skip listener untuk drop/pickup biasa)

## Testing Checklist
- [ ] Ambil elytra pertama kali → notif "elytra ke-1" muncul di WA
- [ ] Ambil elytra kedua (dari End Ship lain) → notif "elytra ke-2", leaderboard update
- [ ] Cek lore item sesuai format
- [ ] Cek PDC bertahan walau item di-rename via anvil
- [ ] Restart server → counter tetap konsisten (baca dari SQLite)
- [ ] Test di client Bedrock (via Geyser) → lore & item behave normal
- [ ] Simulasi webhook WA bot down/timeout → server tidak lag/freeze (harus async & fail gracefully)

## Deliverable
- Maven project lengkap dengan `pom.xml`, `plugin.yml`, struktur package rapi (`listener/`, `db/`, `bridge/`, `command/`, `config/`)
- `config.yml` default dengan komentar penjelasan tiap opsion
- README singkat cara install & konfigurasi webhook URL ke WA bot existing