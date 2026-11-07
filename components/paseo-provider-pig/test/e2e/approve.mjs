// A Pi extension that asks before a bash tool call runs. It loads unchanged in Pi and in PiG.
export default function approve(pi) {
  pi.on("tool_call", async (event, ctx) => {
    if (event.toolName !== "bash") return undefined;
    const ok = await ctx.ui.confirm("Allow bash?", String(event.input?.command ?? ""));
    return ok ? undefined : { block: true, reason: "Blocked by the user" };
  });
}
