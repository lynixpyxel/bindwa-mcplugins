package com.dozzy.http;

public class SendOtpResult {

    public enum Status {
        SUCCESS,
        COOLDOWN,
        INVALID_FORMAT,
        SERVICE_UNAVAILABLE,
        UNAUTHORIZED,
        ERROR
    }

    private final Status status;
    private final int httpStatus;
    private final String message;

    public SendOtpResult(Status status, int httpStatus, String message) {
        this.status = status;
        this.httpStatus = httpStatus;
        this.message = message;
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

    public boolean isSuccess() {
        return status == Status.SUCCESS;
    }
}
