export function canonicalizeTelegramLink(value: string) {
  try {
    const url = new URL(value);
    url.hash = "";
    url.pathname = url.pathname.replace(/\/+$/, "");
    return url.toString().replace(/\/$/, "");
  } catch {
    return value;
  }
}
export function isTelegramMessageLink(value: string) {
  try {
    const url = new URL(value);
    return (
      url.protocol === "https:" &&
      ["t.me", "telegram.me"].includes(url.hostname.toLowerCase()) &&
      /^\/(?:c\/\d+|[A-Za-z][A-Za-z0-9_]{4,31})(?:\/\d+){1,2}\/?$/.test(
        url.pathname,
      )
    );
  } catch {
    return false;
  }
}
export function validateTelegramLinks(input: string | string[]) {
  const links = (Array.isArray(input) ? input : input.split(/\s+/))
    .map((x) => x.trim())
    .filter(Boolean);
  const fail = (message: string): never => {
    throw Object.assign(new Error(message), { code: "INVALID_INPUT" });
  };
  if (links.length > 200) fail("每批最多 200 条消息链接");
  if (!links.length) fail("请先填写消息链接");
  const result: string[] = [],
    seen = new Set<string>();
  for (const link of links) {
    if (!isTelegramMessageLink(link))
      fail("请输入 HTTPS 具体消息链接，不能只填写频道地址：" + link);
    const key = canonicalizeTelegramLink(link);
    if (!seen.has(key)) {
      seen.add(key);
      result.push(link);
    }
  }
  return result;
}
