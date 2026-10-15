// Consumer of the eligible logical-unit response frozen in contract 6233234.
// Eligibility and platform group membership are decided by Java, never by this UI.
export interface SavedMessage {
  id: string;
  externalId: string;
  eligible: true;
  messageIds: string[];
  groupId: string | null;
  text: string;
  sentAt: string;
  attachments: Record<string, any>[];
  sourceLinks: string[];
  downloadSummary: { state: string };
}
interface Reader { get<T>(path: string): Promise<T> }
function requireEligible(message: SavedMessage) {
  if (message.eligible !== true) throw new Error("收藏列表未返回已确认的下载资格，请刷新后重试");
  return message;
}
export function createSavedMessagesApi(client: Reader) {
  return {
    async list(query: string, cursor?: string | null) {
      const params = new URLSearchParams();
      if (query.trim()) params.set("q", query.trim());
      if (cursor) params.set("cursor", cursor);
      const suffix = params.toString();
      const page = await client.get<{ items: SavedMessage[]; total: number | null; nextCursor: string | null }>(
        "/telegram/messages" + (suffix ? "?" + suffix : ""),
      );
      return { ...page, items: page.items.map(requireEligible) };
    },
    async detail(id: string) {
      return requireEligible(await client.get<SavedMessage>("/telegram/messages/" + encodeURIComponent(id)));
    },
  };
}
