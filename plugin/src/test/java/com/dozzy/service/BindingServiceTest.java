package com.dozzy.service;

import com.dozzy.config.PluginConfig;
import org.bukkit.configuration.file.YamlConfiguration;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.util.UUID;

import static org.junit.jupiter.api.Assertions.*;

public class BindingServiceTest {

    private BindingService bindingService;

    @BeforeEach
    void setUp() {
        YamlConfiguration yaml = new YamlConfiguration();
        yaml.set("phone.regex", "^62[0-9]{8,13}$");
        PluginConfig config = new PluginConfig(yaml);
        bindingService = new BindingService(null, config, null, null);
    }

    @Test
    void testPhoneNormalization() {
        // Format lokal 08...
        assertEquals("6281234567890", bindingService.normalizePhone("081234567890"));
        // Tangani jika pemain mengetik di Anvil tanpa hapus default 08
        assertEquals("6281234567890", bindingService.normalizePhone("08081234567890"));
        assertEquals("6281234567890", bindingService.normalizePhone("086281234567890"));
        // Format dengan prefix 8...
        assertEquals("6281234567890", bindingService.normalizePhone("81234567890"));
        // Format dengan +62 dan spasi / strip
        assertEquals("6281234567890", bindingService.normalizePhone("+62 812-3456-7890"));
        // Format standar 62...
        assertEquals("6281234567890", bindingService.normalizePhone("6281234567890"));
    }

    @Test
    void testPhoneValidation() {
        // Valid
        assertTrue(bindingService.isValidPhone("628123456789"));
        assertTrue(bindingService.isValidPhone("628123456789012")); // 15 digits total

        // Invalid (terlalu pendek)
        assertFalse(bindingService.isValidPhone("6281234"));
        // Invalid (terlalu panjang)
        assertFalse(bindingService.isValidPhone("6281234567890123456"));
        // Invalid (bukan awalan 62)
        assertFalse(bindingService.isValidPhone("08123456789"));
        // Invalid (ada karakter non-digit)
        assertFalse(bindingService.isValidPhone("62812abc345"));
    }

    @Test
    void testPendingSessionManagement() {
        UUID uuid = UUID.randomUUID();
        String phone = "628123456789";

        assertTrue(bindingService.getPendingSession(uuid).isEmpty());

        bindingService.setPendingSession(uuid, phone);
        assertTrue(bindingService.getPendingSession(uuid).isPresent());
        assertEquals(phone, bindingService.getPendingSession(uuid).get().getPhone());

        bindingService.removePendingSession(uuid);
        assertTrue(bindingService.getPendingSession(uuid).isEmpty());
    }
}
