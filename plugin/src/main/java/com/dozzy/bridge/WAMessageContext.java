package com.dozzy.bridge;

public class WAMessageContext {

    private final String shortId;
    private final String fullMsgId;
    private final String groupJid;
    private final String groupName;
    private final String senderPhone;
    private final String senderJid;
    private final String pushName;
    private final String text;
    private final long timestamp;

    public WAMessageContext(String shortId, String fullMsgId, String groupJid, String groupName, String senderPhone, String senderJid, String pushName, String text) {
        this.shortId = shortId;
        this.fullMsgId = fullMsgId;
        this.groupJid = groupJid;
        this.groupName = groupName;
        this.senderPhone = senderPhone;
        this.senderJid = senderJid;
        this.pushName = pushName;
        this.text = text;
        this.timestamp = System.currentTimeMillis();
    }

    public String getShortId() {
        return shortId;
    }

    public String getFullMsgId() {
        return fullMsgId;
    }

    public String getGroupJid() {
        return groupJid;
    }

    public String getGroupName() {
        return groupName;
    }

    public String getSenderPhone() {
        return senderPhone;
    }

    public String getSenderJid() {
        return senderJid;
    }

    public String getPushName() {
        return pushName;
    }

    public String getText() {
        return text;
    }

    public long getTimestamp() {
        return timestamp;
    }
}
