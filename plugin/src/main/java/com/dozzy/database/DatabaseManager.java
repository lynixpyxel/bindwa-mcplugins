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

    public synchronized void close() {
        if (connection != null) {
            try {
                connection.close();
            } catch (SQLException ignored) {
            }
        }
    }
}
