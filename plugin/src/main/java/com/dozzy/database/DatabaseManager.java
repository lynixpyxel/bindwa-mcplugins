package com.dozzy.database;

import com.google.gson.Gson;
import com.google.gson.GsonBuilder;
import com.google.gson.JsonElement;
import com.google.gson.JsonObject;

import java.io.File;
import java.io.FileWriter;
import java.io.IOException;
import java.sql.*;
import java.util.*;
import java.util.logging.Level;
import java.util.logging.Logger;

public class DatabaseManager {

    private final File dataFolder;
    private final File dbFile;
    private final File jsonFile;
    private final Logger logger;
    private Connection connection;
    private final Gson gson = new GsonBuilder().setPrettyPrinting().create();

    public DatabaseManager(File dataFolder, Logger logger) {
        this.dataFolder = dataFolder;
        this.dbFile = new File(dataFolder, "wa_binding.db");
        this.jsonFile = new File(dataFolder, "bindings.json");
        this.logger = logger;
    }

    public synchronized void initialize() throws SQLException {
        if (!dbFile.getParentFile().exists()) {
            //noinspection ResultOfMethodCallIgnored
            dbFile.getParentFile().mkdirs();
        }

        try {
            Class.forName("org.sqlite.JDBC");
        } catch (ClassNotFoundException e) {
            throw new SQLException("SQLite JDBC Driver not found", e);
        }

        String url = "jdbc:sqlite:" + dbFile.getAbsolutePath();
        this.connection = DriverManager.getConnection(url);

        try (Statement stmt = this.connection.createStatement()) {
            stmt.execute("PRAGMA journal_mode = WAL;");
            stmt.execute("PRAGMA busy_timeout = 5000;");

            // Tabel wa_binding sesuai schema.md
            stmt.execute("""
                CREATE TABLE IF NOT EXISTS wa_binding (
                    uuid TEXT PRIMARY KEY,
                    phone TEXT NOT NULL UNIQUE,
                    verified INTEGER DEFAULT 0,
                    reward_claimed INTEGER DEFAULT 0,
                    created_at INTEGER,
                    verified_at INTEGER
                );
            """);

            // Tabel wa_bind_log sesuai schema.md
            stmt.execute("""
                CREATE TABLE IF NOT EXISTS wa_bind_log (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    uuid TEXT NOT NULL,
                    phone TEXT NOT NULL,
                    action TEXT NOT NULL,
                    timestamp INTEGER
                );
            """);

            // Tabel elytra_log dan player_elytra_count sesuai elytra-prompt.md
            stmt.execute("""
                CREATE TABLE IF NOT EXISTS elytra_log (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    item_uuid TEXT NOT NULL,
                    owner_uuid TEXT NOT NULL,
                    owner_name TEXT NOT NULL,
                    pickup_number INTEGER NOT NULL,
                    timestamp INTEGER NOT NULL,
                    world TEXT NOT NULL,
                    x INTEGER NOT NULL,
                    y INTEGER NOT NULL,
                    z INTEGER NOT NULL
                );
            """);

            stmt.execute("""
                CREATE TABLE IF NOT EXISTS player_elytra_count (
                    owner_uuid TEXT PRIMARY KEY,
                    owner_name TEXT NOT NULL,
                    total_count INTEGER NOT NULL DEFAULT 0
                );
            """);

            // Tabel imagemap_claims untuk pesanan map dari bot WhatsApp
            stmt.execute("""
                CREATE TABLE IF NOT EXISTS imagemap_claims (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    order_id TEXT NOT NULL UNIQUE,
                    map_name TEXT NOT NULL,
                    player_name TEXT,
                    sender_phone TEXT,
                    image_url TEXT,
                    width INTEGER DEFAULT 1,
                    height INTEGER DEFAULT 1,
                    claimed INTEGER DEFAULT 0,
                    created_at INTEGER,
                    claimed_at INTEGER
                );
            """);

            try {
                stmt.execute("ALTER TABLE imagemap_claims ADD COLUMN image_url TEXT;");
            } catch (SQLException ignored) {}
            try {
                stmt.execute("ALTER TABLE imagemap_claims ADD COLUMN width INTEGER DEFAULT 1;");
            } catch (SQLException ignored) {}
            try {
                stmt.execute("ALTER TABLE imagemap_claims ADD COLUMN height INTEGER DEFAULT 1;");
            } catch (SQLException ignored) {}
        }

        syncToJson();

        logger.info("Database SQLite & JSON bindings berhasil diinisialisasi: " + dbFile.getName() + " & " + jsonFile.getName());
    }

    private synchronized Connection getConnection() throws SQLException {
        if (connection == null || connection.isClosed()) {
            String url = "jdbc:sqlite:" + dbFile.getAbsolutePath();
            connection = DriverManager.getConnection(url);
        }
        return connection;
    }

    public synchronized Optional<WABinding> getBinding(UUID uuid) {
        String sql = "SELECT uuid, phone, verified, reward_claimed, created_at, verified_at FROM wa_binding WHERE uuid = ?";
        try (PreparedStatement ps = getConnection().prepareStatement(sql)) {
            ps.setString(1, uuid.toString());
            try (ResultSet rs = ps.executeQuery()) {
                if (rs.next()) {
                    long verifiedAtVal = rs.getLong("verified_at");
                    Long verifiedAt = rs.wasNull() ? null : verifiedAtVal;
                    return Optional.of(new WABinding(
                            UUID.fromString(rs.getString("uuid")),
                            rs.getString("phone"),
                            rs.getInt("verified") == 1,
                            rs.getInt("reward_claimed") == 1,
                            rs.getLong("created_at"),
                            verifiedAt
                    ));
                }
            }
        } catch (SQLException e) {
            logger.log(Level.SEVERE, "Gagal mengambil data binding untuk UUID: " + uuid, e);
        }
        return Optional.empty();
    }

    public synchronized Optional<WABinding> getBindingByPhone(String phone) {
        String sql = "SELECT uuid, phone, verified, reward_claimed, created_at, verified_at FROM wa_binding WHERE phone = ?";
        try (PreparedStatement ps = getConnection().prepareStatement(sql)) {
            ps.setString(1, phone);
            try (ResultSet rs = ps.executeQuery()) {
                if (rs.next()) {
                    long verifiedAtVal = rs.getLong("verified_at");
                    Long verifiedAt = rs.wasNull() ? null : verifiedAtVal;
                    return Optional.of(new WABinding(
                            UUID.fromString(rs.getString("uuid")),
                            rs.getString("phone"),
                            rs.getInt("verified") == 1,
                            rs.getInt("reward_claimed") == 1,
                            rs.getLong("created_at"),
                            verifiedAt
                    ));
                }
            }
        } catch (SQLException e) {
            logger.log(Level.SEVERE, "Gagal mengambil data binding untuk phone: " + phone, e);
        }
        return Optional.empty();
    }

    public synchronized boolean isBoundAndVerified(UUID uuid) {
        return getBinding(uuid).map(WABinding::isVerified).orElse(false);
    }

    public synchronized boolean isPhoneTakenByOther(String phone, UUID excludeUuid) {
        String sql = "SELECT uuid, verified FROM wa_binding WHERE phone = ?";
        try (PreparedStatement ps = getConnection().prepareStatement(sql)) {
            ps.setString(1, phone);
            try (ResultSet rs = ps.executeQuery()) {
                if (rs.next()) {
                    String existingUuid = rs.getString("uuid");
                    boolean verified = rs.getInt("verified") == 1;
                    // Nomor dianggap taken jika sudah dipakai oleh UUID lain dan verified
                    if (!existingUuid.equalsIgnoreCase(excludeUuid.toString()) && verified) {
                        return true;
                    }
                }
            }
        } catch (SQLException e) {
            logger.log(Level.SEVERE, "Gagal mengecek nomor phone: " + phone, e);
        }
        return false;
    }

    public synchronized void saveOrUpdatePendingBinding(UUID uuid, String phone) {
        long now = System.currentTimeMillis() / 1000L;
        // 1. Bersihkan record unverified lama untuk nomor yang sama jika ada dari UUID lain
        String cleanupSql = "DELETE FROM wa_binding WHERE phone = ? AND verified = 0 AND uuid != ?";
        try (PreparedStatement ps = getConnection().prepareStatement(cleanupSql)) {
            ps.setString(1, phone);
            ps.setString(2, uuid.toString());
            ps.executeUpdate();
        } catch (SQLException e) {
            logger.log(Level.WARNING, "Gagal membersihkan pending phone lama: " + phone, e);
        }

        // 2. Simpan atau perbarui pending untuk UUID ini
        String sql = """
            INSERT INTO wa_binding (uuid, phone, verified, reward_claimed, created_at, verified_at)
            VALUES (?, ?, 0, 0, ?, NULL)
            ON CONFLICT(uuid) DO UPDATE SET
                phone = excluded.phone,
                created_at = excluded.created_at
            WHERE verified = 0;
        """;
        try (PreparedStatement ps = getConnection().prepareStatement(sql)) {
            ps.setString(1, uuid.toString());
            ps.setString(2, phone);
            ps.setLong(3, now);
            ps.executeUpdate();
            syncToJson();
        } catch (SQLException e) {
            logger.log(Level.SEVERE, "Gagal menyimpan pending binding untuk UUID: " + uuid, e);
        }
    }

    public synchronized void setVerified(UUID uuid, String phone) {
        long now = System.currentTimeMillis() / 1000L;
        // Pastikan tidak ada record unverified lain dengan nomor yang sama sebelum verifikasi
        try (PreparedStatement ps = getConnection().prepareStatement("DELETE FROM wa_binding WHERE phone = ? AND uuid != ? AND verified = 0")) {
            ps.setString(1, phone);
            ps.setString(2, uuid.toString());
            ps.executeUpdate();
        } catch (SQLException ignored) {}

        String sql = """
            INSERT INTO wa_binding (uuid, phone, verified, reward_claimed, created_at, verified_at)
            VALUES (?, ?, 1, 0, ?, ?)
            ON CONFLICT(uuid) DO UPDATE SET
                phone = excluded.phone,
                verified = 1,
                verified_at = excluded.verified_at;
        """;
        try (PreparedStatement ps = getConnection().prepareStatement(sql)) {
            ps.setString(1, uuid.toString());
            ps.setString(2, phone);
            ps.setLong(3, now);
            ps.setLong(4, now);
            ps.executeUpdate();
            syncToJson();
        } catch (SQLException e) {
            logger.log(Level.SEVERE, "Gagal update status verified untuk UUID: " + uuid, e);
        }
    }

    public synchronized boolean claimReward(UUID uuid) {
        // Hanya claim jika reward_claimed masih 0
        String checkSql = "SELECT reward_claimed FROM wa_binding WHERE uuid = ?";
        try (PreparedStatement ps = getConnection().prepareStatement(checkSql)) {
            ps.setString(1, uuid.toString());
            try (ResultSet rs = ps.executeQuery()) {
                if (rs.next() && rs.getInt("reward_claimed") == 0) {
                    String updateSql = "UPDATE wa_binding SET reward_claimed = 1 WHERE uuid = ?";
                    try (PreparedStatement updatePs = getConnection().prepareStatement(updateSql)) {
                        updatePs.setString(1, uuid.toString());
                        int rows = updatePs.executeUpdate();
                        if (rows > 0) {
                            syncToJson();
                            return true;
                        }
                    }
                }
            }
        } catch (SQLException e) {
            logger.log(Level.SEVERE, "Gagal claim reward untuk UUID: " + uuid, e);
        }
        return false;
    }

    public synchronized void logAction(UUID uuid, String phone, String action) {
        long now = System.currentTimeMillis() / 1000L;
        String sql = "INSERT INTO wa_bind_log (uuid, phone, action, timestamp) VALUES (?, ?, ?, ?)";
        try (PreparedStatement ps = getConnection().prepareStatement(sql)) {
            ps.setString(1, uuid.toString());
            ps.setString(2, phone);
            ps.setString(3, action);
            ps.setLong(4, now);
            ps.executeUpdate();
        } catch (SQLException e) {
            logger.log(Level.WARNING, "Gagal menulis log action " + action + " untuk UUID: " + uuid, e);
        }
    }

    public synchronized boolean unbind(UUID uuid) {
        String sql = "DELETE FROM wa_binding WHERE uuid = ?";
        try (PreparedStatement ps = getConnection().prepareStatement(sql)) {
            ps.setString(1, uuid.toString());
            int rows = ps.executeUpdate();
            if (rows > 0) {
                syncToJson();
                return true;
            }
        } catch (SQLException e) {
            logger.log(Level.SEVERE, "Gagal unbind UUID: " + uuid, e);
        }
        return false;
    }

    public synchronized void syncToJson() {
        String sql = "SELECT uuid, phone, verified, reward_claimed, created_at, verified_at FROM wa_binding ORDER BY created_at ASC";
        Map<String, Map<String, Object>> dataMap = new LinkedHashMap<>();

        try (PreparedStatement ps = getConnection().prepareStatement(sql);
             ResultSet rs = ps.executeQuery()) {
            while (rs.next()) {
                String uuidStr = rs.getString("uuid");
                String phoneStr = rs.getString("phone");
                boolean verified = rs.getInt("verified") == 1;
                boolean rewardClaimed = rs.getInt("reward_claimed") == 1;
                long createdAt = rs.getLong("created_at");
                long verifiedAtVal = rs.getLong("verified_at");
                Long verifiedAt = rs.wasNull() ? null : verifiedAtVal;

                Map<String, Object> entry = new LinkedHashMap<>();
                entry.put("uuid", uuidStr);
                entry.put("phone", phoneStr);
                entry.put("verified", verified);
                entry.put("reward_claimed", rewardClaimed);
                entry.put("created_at", createdAt);
                entry.put("verified_at", verifiedAt);

                dataMap.put(uuidStr, entry);
            }

            try (FileWriter writer = new FileWriter(jsonFile)) {
                gson.toJson(dataMap, writer);
            } catch (IOException e) {
                logger.log(Level.WARNING, "Gagal menulis file bindings.json: " + e.getMessage());
            }

        } catch (SQLException e) {
            logger.log(Level.WARNING, "Gagal sinkronisasi data ke bindings.json: " + e.getMessage());
        }
    }

    public record ElytraLeaderboardEntry(String name, int count) {}

    public synchronized int incrementAndGetElytraCount(UUID ownerUuid, String ownerName) {
        String upsertSql = """
            INSERT INTO player_elytra_count (owner_uuid, owner_name, total_count)
            VALUES (?, ?, 1)
            ON CONFLICT(owner_uuid) DO UPDATE SET
                owner_name = excluded.owner_name,
                total_count = total_count + 1;
        """;
        try (PreparedStatement ps = getConnection().prepareStatement(upsertSql)) {
            ps.setString(1, ownerUuid.toString());
            ps.setString(2, ownerName);
            ps.executeUpdate();
        } catch (SQLException e) {
            logger.log(Level.SEVERE, "Gagal increment elytra count untuk " + ownerName, e);
        }

        return getPlayerElytraCount(ownerUuid);
    }

    public synchronized int getPlayerElytraCount(UUID ownerUuid) {
        String sql = "SELECT total_count FROM player_elytra_count WHERE owner_uuid = ?";
        try (PreparedStatement ps = getConnection().prepareStatement(sql)) {
            ps.setString(1, ownerUuid.toString());
            try (ResultSet rs = ps.executeQuery()) {
                if (rs.next()) {
                    return rs.getInt("total_count");
                }
            }
        } catch (SQLException e) {
            logger.log(Level.SEVERE, "Gagal mengambil elytra count untuk UUID: " + ownerUuid, e);
        }
        return 0;
    }

    public synchronized int decrementElytraCount(UUID ownerUuid) {
        String sql = "UPDATE player_elytra_count SET total_count = MAX(0, total_count - 1) WHERE owner_uuid = ?";
        try (PreparedStatement ps = getConnection().prepareStatement(sql)) {
            ps.setString(1, ownerUuid.toString());
            ps.executeUpdate();
        } catch (SQLException e) {
            logger.log(Level.SEVERE, "Gagal decrement elytra count untuk UUID: " + ownerUuid, e);
        }
        return getPlayerElytraCount(ownerUuid);
    }

    public synchronized String getPlayerNameByUuid(UUID uuid) {
        String sql = "SELECT owner_name FROM player_elytra_count WHERE owner_uuid = ?";
        try (PreparedStatement ps = getConnection().prepareStatement(sql)) {
            ps.setString(1, uuid.toString());
            try (ResultSet rs = ps.executeQuery()) {
                if (rs.next()) {
                    return rs.getString("owner_name");
                }
            }
        } catch (SQLException ignored) {}
        return "Player";
    }

    public synchronized void logElytraPickup(UUID itemUuid, UUID ownerUuid, String ownerName, int pickupNumber, String world, int x, int y, int z) {
        long now = System.currentTimeMillis();
        String sql = "INSERT INTO elytra_log (item_uuid, owner_uuid, owner_name, pickup_number, timestamp, world, x, y, z) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)";
        try (PreparedStatement ps = getConnection().prepareStatement(sql)) {
            ps.setString(1, itemUuid.toString());
            ps.setString(2, ownerUuid.toString());
            ps.setString(3, ownerName);
            ps.setInt(4, pickupNumber);
            ps.setLong(5, now);
            ps.setString(6, world);
            ps.setInt(7, x);
            ps.setInt(8, y);
            ps.setInt(9, z);
            ps.executeUpdate();
        } catch (SQLException e) {
            logger.log(Level.WARNING, "Gagal menulis log pickup elytra untuk " + ownerName, e);
        }
    }

    public synchronized List<ElytraLeaderboardEntry> getElytraLeaderboard(int limit) {
        List<ElytraLeaderboardEntry> list = new ArrayList<>();
        String sql = "SELECT owner_name, total_count FROM player_elytra_count ORDER BY total_count DESC LIMIT ?";
        try (PreparedStatement ps = getConnection().prepareStatement(sql)) {
            ps.setInt(1, Math.max(1, limit));
            try (ResultSet rs = ps.executeQuery()) {
                while (rs.next()) {
                    list.add(new ElytraLeaderboardEntry(rs.getString("owner_name"), rs.getInt("total_count")));
                }
            }
        } catch (SQLException e) {
            logger.log(Level.SEVERE, "Gagal mengambil leaderboard elytra", e);
        }
        return list;
    }

    public synchronized void resetAllElytraLeaderboard() {
        try (Statement stmt = getConnection().createStatement()) {
            stmt.execute("DELETE FROM player_elytra_count;");
            stmt.execute("DELETE FROM elytra_log;");
        } catch (SQLException e) {
            logger.log(Level.SEVERE, "Gagal mereset semua leaderboard elytra", e);
        }
    }

    public synchronized void resetPlayerElytra(UUID ownerUuid) {
        try (PreparedStatement ps1 = getConnection().prepareStatement("DELETE FROM player_elytra_count WHERE owner_uuid = ?");
             PreparedStatement ps2 = getConnection().prepareStatement("DELETE FROM elytra_log WHERE owner_uuid = ?")) {
            ps1.setString(1, ownerUuid.toString());
            ps1.executeUpdate();
            ps2.setString(1, ownerUuid.toString());
            ps2.executeUpdate();
        } catch (SQLException e) {
            logger.log(Level.SEVERE, "Gagal mereset elytra untuk UUID: " + ownerUuid, e);
        }
    }

    public synchronized void saveImageMapClaim(String orderId, String mapName, String playerName, String senderPhone, String imageUrl, int width, int height) {
        long now = System.currentTimeMillis() / 1000L;
        String sql = """
            INSERT INTO imagemap_claims (order_id, map_name, player_name, sender_phone, image_url, width, height, claimed, created_at, claimed_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, NULL)
            ON CONFLICT(order_id) DO UPDATE SET
                map_name = excluded.map_name,
                player_name = COALESCE(excluded.player_name, imagemap_claims.player_name),
                sender_phone = excluded.sender_phone,
                image_url = COALESCE(excluded.image_url, imagemap_claims.image_url),
                width = excluded.width,
                height = excluded.height;
        """;
        try (PreparedStatement ps = getConnection().prepareStatement(sql)) {
            ps.setString(1, orderId);
            ps.setString(2, mapName);
            ps.setString(3, playerName);
            ps.setString(4, senderPhone);
            ps.setString(5, imageUrl);
            ps.setInt(6, width);
            ps.setInt(7, height);
            ps.setLong(8, now);
            ps.executeUpdate();
        } catch (SQLException e) {
            logger.log(Level.SEVERE, "Gagal menyimpan claim imagemap untuk order: " + orderId, e);
        }
    }

    public synchronized void assignPlayerToClaim(String orderId, String playerName, String imageUrl, int width, int height) {
        String sql = """
            UPDATE imagemap_claims
            SET player_name = ?,
                image_url = COALESCE(?, image_url),
                width = CASE WHEN ? > 0 THEN ? ELSE width END,
                height = CASE WHEN ? > 0 THEN ? ELSE height END
            WHERE order_id = ?
        """;
        try (PreparedStatement ps = getConnection().prepareStatement(sql)) {
            ps.setString(1, playerName);
            ps.setString(2, imageUrl);
            ps.setInt(3, width);
            ps.setInt(4, width);
            ps.setInt(5, height);
            ps.setInt(6, height);
            ps.setString(7, orderId);
            ps.executeUpdate();
        } catch (SQLException e) {
            logger.log(Level.SEVERE, "Gagal menetapkan player ke claim imagemap order: " + orderId, e);
        }
    }

    public synchronized List<ImageMapClaim> getUnclaimedImageMaps(String playerName) {
        List<ImageMapClaim> list = new ArrayList<>();
        if (playerName == null || playerName.trim().isEmpty()) {
            return list;
        }
        String clean = playerName.trim();
        String alt = clean.startsWith(".") ? clean.substring(1) : "." + clean;

        String sql = "SELECT id, order_id, map_name, player_name, sender_phone, image_url, width, height, claimed, created_at, claimed_at FROM imagemap_claims WHERE (LOWER(player_name) = LOWER(?) OR LOWER(player_name) = LOWER(?)) AND claimed = 0 ORDER BY id ASC";
        try (PreparedStatement ps = getConnection().prepareStatement(sql)) {
            ps.setString(1, clean);
            ps.setString(2, alt);
            try (ResultSet rs = ps.executeQuery()) {
                while (rs.next()) {
                    long claimedAtVal = rs.getLong("claimed_at");
                    Long claimedAt = rs.wasNull() ? null : claimedAtVal;
                    list.add(new ImageMapClaim(
                            rs.getInt("id"),
                            rs.getString("order_id"),
                            rs.getString("map_name"),
                            rs.getString("player_name"),
                            rs.getString("sender_phone"),
                            rs.getString("image_url"),
                            rs.getInt("width"),
                            rs.getInt("height"),
                            rs.getInt("claimed") == 1,
                            rs.getLong("created_at"),
                            claimedAt
                    ));
                }
            }
        } catch (SQLException e) {
            logger.log(Level.SEVERE, "Gagal mengambil data claim imagemap untuk player: " + playerName, e);
        }
        return list;
    }

    public synchronized List<ImageMapClaim> getAllClaimsByPlayer(String playerName) {
        List<ImageMapClaim> list = new ArrayList<>();
        if (playerName == null || playerName.trim().isEmpty()) {
            return list;
        }
        String clean = playerName.trim();
        String alt = clean.startsWith(".") ? clean.substring(1) : "." + clean;

        String sql = "SELECT id, order_id, map_name, player_name, sender_phone, image_url, width, height, claimed, created_at, claimed_at FROM imagemap_claims WHERE (LOWER(player_name) = LOWER(?) OR LOWER(player_name) = LOWER(?)) ORDER BY id DESC";
        try (PreparedStatement ps = getConnection().prepareStatement(sql)) {
            ps.setString(1, clean);
            ps.setString(2, alt);
            try (ResultSet rs = ps.executeQuery()) {
                while (rs.next()) {
                    long claimedAtVal = rs.getLong("claimed_at");
                    Long claimedAt = rs.wasNull() ? null : claimedAtVal;
                    list.add(new ImageMapClaim(
                            rs.getInt("id"),
                            rs.getString("order_id"),
                            rs.getString("map_name"),
                            rs.getString("player_name"),
                            rs.getString("sender_phone"),
                            rs.getString("image_url"),
                            rs.getInt("width"),
                            rs.getInt("height"),
                            rs.getInt("claimed") == 1,
                            rs.getLong("created_at"),
                            claimedAt
                    ));
                }
            }
        } catch (SQLException e) {
            logger.log(Level.SEVERE, "Gagal mengambil riwayat claim imagemap untuk player: " + playerName, e);
        }
        return list;
    }

    public synchronized boolean markImageMapClaimed(int id) {
        long now = System.currentTimeMillis() / 1000L;
        String sql = "UPDATE imagemap_claims SET claimed = 1, claimed_at = ? WHERE id = ? AND claimed = 0";
        try (PreparedStatement ps = getConnection().prepareStatement(sql)) {
            ps.setLong(1, now);
            ps.setInt(2, id);
            return ps.executeUpdate() > 0;
        } catch (SQLException e) {
            logger.log(Level.SEVERE, "Gagal menandai claim imagemap id " + id + " sebagai claimed", e);
        }
        return false;
    }

    public synchronized boolean resetClaimStatus(int id) {
        String sql = "UPDATE imagemap_claims SET claimed = 0, claimed_at = NULL WHERE id = ?";
        try (PreparedStatement ps = getConnection().prepareStatement(sql)) {
            ps.setInt(1, id);
            return ps.executeUpdate() > 0;
        } catch (SQLException e) {
            logger.log(Level.SEVERE, "Gagal mereset status claim imagemap id " + id, e);
        }
        return false;
    }

    public synchronized Optional<ImageMapClaim> getClaimByOrderId(String orderId) {
        String sql = "SELECT id, order_id, map_name, player_name, sender_phone, image_url, width, height, claimed, created_at, claimed_at FROM imagemap_claims WHERE order_id = ?";
        try (PreparedStatement ps = getConnection().prepareStatement(sql)) {
            ps.setString(1, orderId);
            try (ResultSet rs = ps.executeQuery()) {
                if (rs.next()) {
                    long claimedAtVal = rs.getLong("claimed_at");
                    Long claimedAt = rs.wasNull() ? null : claimedAtVal;
                    return Optional.of(new ImageMapClaim(
                            rs.getInt("id"),
                            rs.getString("order_id"),
                            rs.getString("map_name"),
                            rs.getString("player_name"),
                            rs.getString("sender_phone"),
                            rs.getString("image_url"),
                            rs.getInt("width"),
                            rs.getInt("height"),
                            rs.getInt("claimed") == 1,
                            rs.getLong("created_at"),
                            claimedAt
                    ));
                }
            }
        } catch (SQLException e) {
            logger.log(Level.SEVERE, "Gagal mengambil claim imagemap order: " + orderId, e);
        }
        return Optional.empty();
    }

    public synchronized void close() {
        if (connection != null) {
            try {
                connection.close();
            } catch (SQLException ignored) {
            }
        }
    }
}
