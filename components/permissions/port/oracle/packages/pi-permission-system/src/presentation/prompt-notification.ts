import { basename } from "node:path";
import type { PromptNotificationChannel } from "#src/config/config-schema";
import type { PromptRequestFacts } from "./prompt-payload";

const BEL = "\x07";
const OSC = "\x1b]";

/** What a prompt notification says: OSC 777 carries both fields. */
export interface PromptNotice {
  readonly title: string;
  readonly body: string;
}

/** The session facts a notice names, read when the prompt opens. */
export interface NotificationSession {
  readonly name: string | undefined;
  readonly cwd: string;
}

/** The request facts a notice may name: labels only, never the value. */
export type NoticeRequestFacts = Pick<
  PromptRequestFacts,
  "toolName" | "surface" | "requester"
>;

/**
 * What a prompt notification says: which session is waiting, and what is
 * being asked and by whom.
 *
 * Labels only. The request's value (the command, path, MCP target, or skill)
 * never appears: the OS notification history is a store this package does not
 * control, outside the owner-only logs and their key-name masking.
 */
export function describePromptNotice(
  dialogTitle: string,
  request: NoticeRequestFacts,
  session: NotificationSession,
): PromptNotice {
  const label = sessionLabel(session);
  const subject = askSubject(request);
  return {
    title: label ? `pi — ${label}` : "pi",
    body: subject ? `${dialogTitle}: ${subject}` : dialogTitle,
  };
}

/** The session's name, or the name of the directory it runs in, as Pi's own terminal title does. */
function sessionLabel(session: NotificationSession): string {
  // eslint-disable-next-line @typescript-eslint/prefer-nullish-coalescing -- || intentional: an empty name is unnamed
  return session.name || basename(session.cwd);
}

/** What is asked (the tool, else the gate surface) and by which agent. */
function askSubject(request: NoticeRequestFacts): string {
  const what = request.toolName ?? request.surface;
  const who = request.requester.agentName;
  if (what && who) return `${what} (${who})`;
  // eslint-disable-next-line @typescript-eslint/prefer-nullish-coalescing -- || intentional: an empty label names nothing
  return what || who || "";
}

/**
 * The bytes that ask a terminal for attention as a permission dialog opens.
 *
 * One sequence per configured channel, in configured order; an empty list
 * renders nothing. Every field is stripped of control characters, so it cannot
 * end a sequence early or start another one.
 */
export function renderPromptNotification(
  channels: readonly PromptNotificationChannel[],
  notice: PromptNotice,
): string {
  const clean: PromptNotice = {
    title: withoutControlCharacters(notice.title),
    body: withoutControlCharacters(notice.body),
  };
  return channels.map((channel) => renderChannel(channel, clean)).join("");
}

function renderChannel(
  channel: PromptNotificationChannel,
  notice: PromptNotice,
): string {
  switch (channel) {
    case "bell":
      return BEL;
    case "osc9":
      // One field, so the title leads it: it is what names the session.
      return `${OSC}9;${notice.title}: ${notice.body}${BEL}`;
    case "osc777":
      return `${OSC}777;notify;${osc777Field(notice.title)};${osc777Field(notice.body)}${BEL}`;
  }
}

/** `;` separates OSC 777's fields, so one inside a field would split it. */
function osc777Field(text: string): string {
  return text.replaceAll(";", ":");
}

function withoutControlCharacters(value: string): string {
  // Code points, not grapheme clusters: a control character is always one.
  return Array.from(value)
    .filter((character) => {
      const code = character.codePointAt(0) ?? 0;
      return code > 31 && code !== 127;
    })
    .join("");
}
