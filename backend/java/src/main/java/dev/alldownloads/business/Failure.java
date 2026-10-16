package dev.alldownloads.business;

public final class Failure extends RuntimeException {
  public final int status;
  public final String code;

  public Failure(int status, String code, String message) {
    super(message);
    this.status = status;
    this.code = code;
  }

  public static Failure bad(String code, String message) {
    return new Failure(400, code, message);
  }

  public static Failure conflict(String code) {
    return new Failure(409, code, "资料或状态已改变，请刷新后重试");
  }

  public static Failure missing() {
    return new Failure(404, "NOT_FOUND", "对象不存在或尚未收录");
  }
}
