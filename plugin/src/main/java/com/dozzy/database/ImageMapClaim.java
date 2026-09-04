package com.dozzy.database;

public class ImageMapClaim {
    private final int id;
    private final String orderId;
    private final String mapName;
    private final String playerName;
    private final String senderPhone;
    private final String imageUrl;
    private final int width;
    private final int height;
    private final boolean claimed;
    private final long createdAt;
    private final Long claimedAt;

    public ImageMapClaim(int id, String orderId, String mapName, String playerName, String senderPhone, String imageUrl, int width, int height, boolean claimed, long createdAt, Long claimedAt) {
        this.id = id;
        this.orderId = orderId;
        this.mapName = mapName;
        this.playerName = playerName;
        this.senderPhone = senderPhone;
        this.imageUrl = imageUrl;
        this.width = width;
        this.height = height;
        this.claimed = claimed;
        this.createdAt = createdAt;
        this.claimedAt = claimedAt;
    }

    public int getId() {
        return id;
    }

    public String getOrderId() {
        return orderId;
    }

    public String getMapName() {
        return mapName;
    }

    public String getPlayerName() {
        return playerName;
    }

    public String getSenderPhone() {
        return senderPhone;
    }

    public String getImageUrl() {
        return imageUrl;
    }

    public int getWidth() {
        return width;
    }

    public int getHeight() {
        return height;
    }

    public boolean isClaimed() {
        return claimed;
    }

    public long getCreatedAt() {
        return createdAt;
    }

    public Long getClaimedAt() {
        return claimedAt;
    }
}
