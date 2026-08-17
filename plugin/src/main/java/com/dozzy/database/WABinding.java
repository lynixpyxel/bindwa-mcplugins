package com.dozzy.database;

import java.util.UUID;

public class WABinding {
    private final UUID uuid;
    private final String phone;
    private final boolean verified;
    private final boolean rewardClaimed;
    private final long createdAt;
    private final Long verifiedAt;

    public WABinding(UUID uuid, String phone, boolean verified, boolean rewardClaimed, long createdAt, Long verifiedAt) {
        this.uuid = uuid;
        this.phone = phone;
        this.verified = verified;
        this.rewardClaimed = rewardClaimed;
        this.createdAt = createdAt;
        this.verifiedAt = verifiedAt;
    }

    public UUID getUuid() {
        return uuid;
    }

    public String getPhone() {
        return phone;
    }

    public boolean isVerified() {
        return verified;
    }

    public boolean isRewardClaimed() {
        return rewardClaimed;
    }

    public long getCreatedAt() {
        return createdAt;
    }

    public Long getVerifiedAt() {
        return verifiedAt;
    }

    public String getMaskedPhone() {
        if (phone == null || phone.length() < 7) {
            return phone;
        }
        int len = phone.length();
        return phone.substring(0, 4) + "****" + phone.substring(len - 3);
    }
}
