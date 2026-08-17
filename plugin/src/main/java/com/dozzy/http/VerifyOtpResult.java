package com.dozzy.http;

public class VerifyOtpResult {

    public enum Status {
        VERIFIED,
        WRONG_OTP,
        EXPIRED_OR_NOT_FOUND,
        MAX_ATTEMPTS_EXCEEDED,
        UNAUTHORIZED,
        ERROR
    }

    private final Status status;
    private final int httpStatus;
    private final String message;
    private final int attemptsLeft;

    public VerifyOtpResult(Status status, int httpStatus, String message, int attemptsLeft) {
        this.status = status;
        this.httpStatus = httpStatus;
        this.message = message;
        this.attemptsLeft = attemptsLeft;
    }

    public Status getStatus() {
        return status;
    }

    public int getHttpStatus() {
        return httpStatus;
    }

    public String getMessage() {
        return message;
    }

    public int getAttemptsLeft() {
        return attemptsLeft;
    }

    public boolean isVerified() {
        return status == Status.VERIFIED;
    }
}
