package com.dozzy.database;

import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.File;
import java.nio.file.Path;
import java.sql.SQLException;
import java.util.Optional;
import java.util.UUID;
import java.util.logging.Logger;

import static org.junit.jupiter.api.Assertions.*;

public class DatabaseManagerTest {

    @TempDir
    Path tempDir;

    private DatabaseManager databaseManager;

    @BeforeEach
    void setUp() throws SQLException {
        File dataFolder = tempDir.toFile();
        Logger logger = Logger.getLogger("DatabaseManagerTest");
        databaseManager = new DatabaseManager(dataFolder, logger);
        databaseManager.initialize();
    }

    @AfterEach
    void tearDown() {
        databaseManager.close();
    }

    @Test
    void testPendingBindingAndVerificationFlow() {
        UUID uuid = UUID.randomUUID();
        String phone = "628123456789";

        // Belum ada binding
        assertFalse(databaseManager.isBoundAndVerified(uuid));
        assertTrue(databaseManager.getBinding(uuid).isEmpty());

        // Simpan pending
        databaseManager.saveOrUpdatePendingBinding(uuid, phone);
        Optional<WABinding> pending = databaseManager.getBinding(uuid);
        assertTrue(pending.isPresent());
        assertEquals(phone, pending.get().getPhone());
        assertFalse(pending.get().isVerified());
        assertFalse(pending.get().isRewardClaimed());
        assertNull(pending.get().getVerifiedAt());

        // Verifikasi sukses
        databaseManager.setVerified(uuid, phone);
        Optional<WABinding> verified = databaseManager.getBinding(uuid);
        assertTrue(verified.isPresent());
        assertTrue(verified.get().isVerified());
        assertNotNull(verified.get().getVerifiedAt());
        assertTrue(databaseManager.isBoundAndVerified(uuid));

        // Claim reward
        assertTrue(databaseManager.claimReward(uuid));
        // Claim reward kedua kali harus gagal (hanya sekali)
        assertFalse(databaseManager.claimReward(uuid));

        Optional<WABinding> rewarded = databaseManager.getBinding(uuid);
        assertTrue(rewarded.isPresent());
        assertTrue(rewarded.get().isRewardClaimed());
    }

    @Test
    void testPhoneUniquenessCheck() {
        UUID uuid1 = UUID.randomUUID();
        UUID uuid2 = UUID.randomUUID();
        String phone = "628999999999";

        // Belum terdaftar
        assertFalse(databaseManager.isPhoneTakenByOther(phone, uuid1));
        assertFalse(databaseManager.isPhoneTakenByOther(phone, uuid2));

        // uuid1 verifikasi nomor phone
        databaseManager.setVerified(uuid1, phone);

        // uuid1 mengecek nomor miliknya -> tidak dianggap taken by other
        assertFalse(databaseManager.isPhoneTakenByOther(phone, uuid1));

        // uuid2 mengecek nomor uuid1 -> dianggap taken by other
        assertTrue(databaseManager.isPhoneTakenByOther(phone, uuid2));
    }

    @Test
    void testMaskedPhone() {
        WABinding binding = new WABinding(UUID.randomUUID(), "628123456789", true, false, 1000L, 1000L);
        assertEquals("6281****789", binding.getMaskedPhone());
    }

    @Test
    void testActionLogging() {
        UUID uuid = UUID.randomUUID();
        String phone = "628555555555";
        assertDoesNotThrow(() -> {
            databaseManager.logAction(uuid, phone, "send_otp");
            databaseManager.logAction(uuid, phone, "verify_success");
        });
    }

    @Test
    void testJsonSync() {
        UUID uuid = UUID.randomUUID();
        String phone = "628777777777";
        databaseManager.setVerified(uuid, phone);

        File jsonFile = new File(tempDir.toFile(), "bindings.json");
        assertTrue(jsonFile.exists());
        assertTrue(jsonFile.length() > 0);
    }
}
