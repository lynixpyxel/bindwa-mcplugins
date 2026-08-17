package com.dozzy.http;

import com.dozzy.config.PluginConfig;

import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import java.util.logging.Level;
import java.util.logging.Logger;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

public class BotApiClient {

    private final PluginConfig config;
    private final Logger logger;
    private final HttpClient httpClient;

    private static final Pattern ATTEMPTS_LEFT_PATTERN = Pattern.compile("\"attempts_left\"\\s*:\\s*(\\d+)");
    private static final Pattern MESSAGE_PATTERN = Pattern.compile("\"message\"\\s*:\\s*\"([^\"]+)\"");

    public BotApiClient(PluginConfig config, Logger logger) {
        this.config = config;
        this.logger = logger;
        this.httpClient = HttpClient.newBuilder()
                .connectTimeout(Duration.ofSeconds(5))
                .build();
    }

    public CompletableFuture<SendOtpResult> sendOtp(UUID uuid, String phone) {
        String url = trimTrailingSlash(config.getApiBaseUrl()) + "/send-otp";
        String jsonBody = String.format("{\"uuid\":\"%s\",\"phone\":\"%s\"}", uuid.toString(), escapeJson(phone));

        HttpRequest request = HttpRequest.newBuilder()
                .uri(URI.create(url))
                .header("Content-Type", "application/json")
                .header("Authorization", "Bearer " + config.getApiToken())
                .timeout(Duration.ofSeconds(15))
                .POST(HttpRequest.BodyPublishers.ofString(jsonBody))
                .build();

        return httpClient.sendAsync(request, HttpResponse.BodyHandlers.ofString())
                .thenApply(response -> {
                    int statusCode = response.statusCode();
                    String body = response.body();
                    String message = extractMessage(body);

                    return switch (statusCode) {
                        case 200 -> new SendOtpResult(SendOtpResult.Status.SUCCESS, statusCode, message);
                        case 400 -> new SendOtpResult(SendOtpResult.Status.INVALID_FORMAT, statusCode, message);
                        case 401 -> {
                            logger.severe("Bot API Token tidak cocok (401 Unauthorized). Cek config.yml dan config.json bot!");
                            yield new SendOtpResult(SendOtpResult.Status.UNAUTHORIZED, statusCode, message);
                        }
                        case 429 -> new SendOtpResult(SendOtpResult.Status.COOLDOWN, statusCode, message);
                        case 502 -> new SendOtpResult(SendOtpResult.Status.SERVICE_UNAVAILABLE, statusCode, message);
                        default -> new SendOtpResult(SendOtpResult.Status.ERROR, statusCode, message);
                    };
                })
                .exceptionally(throwable -> {
                    logger.log(Level.WARNING, "Gagal menghubungi bot WhatsApp API di " + url + ": " + throwable.getMessage());
                    return new SendOtpResult(SendOtpResult.Status.SERVICE_UNAVAILABLE, 0, throwable.getMessage());
                });
    }

    public CompletableFuture<VerifyOtpResult> verifyOtp(UUID uuid, String phone, String otp) {
        String url = trimTrailingSlash(config.getApiBaseUrl()) + "/verify-otp";
        String jsonBody = String.format("{\"uuid\":\"%s\",\"phone\":\"%s\",\"otp\":\"%s\"}",
                uuid.toString(), escapeJson(phone), escapeJson(otp));

        HttpRequest request = HttpRequest.newBuilder()
                .uri(URI.create(url))
                .header("Content-Type", "application/json")
                .header("Authorization", "Bearer " + config.getApiToken())
                .timeout(Duration.ofSeconds(15))
                .POST(HttpRequest.BodyPublishers.ofString(jsonBody))
                .build();

        return httpClient.sendAsync(request, HttpResponse.BodyHandlers.ofString())
                .thenApply(response -> {
                    int statusCode = response.statusCode();
                    String body = response.body();
                    String message = extractMessage(body);
                    int attemptsLeft = extractAttemptsLeft(body);

                    return switch (statusCode) {
                        case 200 -> new VerifyOtpResult(VerifyOtpResult.Status.VERIFIED, statusCode, message, attemptsLeft);
                        case 400 -> new VerifyOtpResult(VerifyOtpResult.Status.WRONG_OTP, statusCode, message, attemptsLeft);
                        case 401 -> {
                            logger.severe("Bot API Token tidak cocok (401 Unauthorized). Cek config.yml dan config.json bot!");
                            yield new VerifyOtpResult(VerifyOtpResult.Status.UNAUTHORIZED, statusCode, message, 0);
                        }
                        case 410 -> new VerifyOtpResult(VerifyOtpResult.Status.EXPIRED_OR_NOT_FOUND, statusCode, message, 0);
                        case 429 -> new VerifyOtpResult(VerifyOtpResult.Status.MAX_ATTEMPTS_EXCEEDED, statusCode, message, 0);
                        default -> new VerifyOtpResult(VerifyOtpResult.Status.ERROR, statusCode, message, attemptsLeft);
                    };
                })
                .exceptionally(throwable -> {
                    logger.log(Level.WARNING, "Gagal menghubungi bot WhatsApp API saat verifikasi di " + url + ": " + throwable.getMessage());
                    return new VerifyOtpResult(VerifyOtpResult.Status.ERROR, 0, throwable.getMessage(), 0);
                });
    }

    public CompletableFuture<Boolean> sendGroupChat(String player, String message) {
        String url = trimTrailingSlash(config.getApiBaseUrl()) + "/send-group-chat";
        String jsonBody = String.format("{\"player\":\"%s\",\"message\":\"%s\"}", escapeJson(player), escapeJson(message));

        HttpRequest request = HttpRequest.newBuilder()
                .uri(URI.create(url))
                .header("Content-Type", "application/json")
                .header("Authorization", "Bearer " + config.getApiToken())
                .timeout(Duration.ofSeconds(10))
                .POST(HttpRequest.BodyPublishers.ofString(jsonBody))
                .build();

        return httpClient.sendAsync(request, HttpResponse.BodyHandlers.ofString())
                .thenApply(res -> res.statusCode() == 200)
                .exceptionally(t -> {
                    logger.log(Level.WARNING, "Gagal kirim group chat ke bot WA: " + t.getMessage());
                    return false;
                });
    }

    public CompletableFuture<Boolean> sendNotification(String title, String message) {
        String url = trimTrailingSlash(config.getApiBaseUrl()) + "/send-notification";
        String jsonBody = String.format("{\"title\":\"%s\",\"message\":\"%s\"}", escapeJson(title), escapeJson(message));

        HttpRequest request = HttpRequest.newBuilder()
                .uri(URI.create(url))
                .header("Content-Type", "application/json")
                .header("Authorization", "Bearer " + config.getApiToken())
                .timeout(Duration.ofSeconds(10))
                .POST(HttpRequest.BodyPublishers.ofString(jsonBody))
                .build();

        return httpClient.sendAsync(request, HttpResponse.BodyHandlers.ofString())
                .thenApply(res -> res.statusCode() == 200)
                .exceptionally(t -> {
                    logger.log(Level.WARNING, "Gagal kirim notifikasi ke bot WA: " + t.getMessage());
                    return false;
                });
    }

    public CompletableFuture<Boolean> sendServerStatus(int playerCount, int maxPlayers, java.util.List<String> playerList, double tps) {
        String url = trimTrailingSlash(config.getApiBaseUrl()) + "/server-status";
        StringBuilder sb = new StringBuilder();
        sb.append("{\"player_count\":").append(playerCount)
                .append(",\"max_players\":").append(maxPlayers)
                .append(",\"tps\":").append(String.format(java.util.Locale.US, "%.2f", tps))
                .append(",\"player_list\":[");
        for (int i = 0; i < playerList.size(); i++) {
            if (i > 0) sb.append(",");
            sb.append("\"").append(escapeJson(playerList.get(i))).append("\"");
        }
        sb.append("]}");

        HttpRequest request = HttpRequest.newBuilder()
                .uri(URI.create(url))
                .header("Content-Type", "application/json")
                .header("Authorization", "Bearer " + config.getApiToken())
                .timeout(Duration.ofSeconds(5))
                .POST(HttpRequest.BodyPublishers.ofString(sb.toString()))
                .build();

        return httpClient.sendAsync(request, HttpResponse.BodyHandlers.ofString())
                .thenApply(res -> res.statusCode() == 200)
                .exceptionally(t -> false);
    }

    private String trimTrailingSlash(String url) {
        if (url.endsWith("/")) {
            return url.substring(0, url.length() - 1);
        }
        return url;
    }

    private String escapeJson(String raw) {
        if (raw == null) return "";
        return raw.replace("\\", "\\\\").replace("\"", "\\\"");
    }

    private String extractMessage(String body) {
        if (body == null || body.isEmpty()) return "";
        Matcher matcher = MESSAGE_PATTERN.matcher(body);
        if (matcher.find()) {
            return matcher.group(1);
        }
        return "";
    }

    private int extractAttemptsLeft(String body) {
        if (body == null || body.isEmpty()) return 0;
        Matcher matcher = ATTEMPTS_LEFT_PATTERN.matcher(body);
        if (matcher.find()) {
            try {
                return Integer.parseInt(matcher.group(1));
            } catch (NumberFormatException ignored) {}
        }
        return 0;
    }
}
