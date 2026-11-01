// The note a deleted session's page leads with (ADR 0014 in paddock), and
// Restore. A chunk of its own: only a deleted session's page shows it.
import { RotateCcwIcon } from "lucide-react";
import { useRef } from "react";

import { useRestoreSession, type Session } from "@/api/hooks";
import { Button } from "@/components/ui/button";
import { sessionDate } from "@/lib/format";

import { failure, useFocus } from "./deleting";

/** The note a deleted session's page leads with, and Restore. `focus` takes
 * the focus to Restore, as after a delete; `onRestored` is told of a restore. */
export function DeletedNote({
  session: s,
  focus = false,
  onRestored,
}: {
  session: Session;
  focus?: boolean;
  onRestored?: () => void;
}) {
  const restore = useRestoreSession(s.session_id, onRestored);
  const button = useRef<HTMLButtonElement>(null);
  useFocus(button, focus);
  return (
    <section
      aria-label="Deleted"
      data-slot="deleted-note"
      className="flex flex-col items-start gap-2 rounded-md border bg-surface p-4"
    >
      <p className="font-semibold">
        Deleted {s.deleted_at != null && <span className="font-normal text-muted">{sessionDate(s.deleted_at)}</span>}
      </p>
      <p className="text-sm text-muted">
        This session isn&apos;t in the session list or lap compare. Restore puts it back.
      </p>
      {restore.error && (
        <p role="alert" className="text-sm">
          {failure(restore.error, "restore")}
        </p>
      )}
      <Button ref={button} variant="outline" disabled={restore.isPending} onClick={() => restore.mutate()}>
        <RotateCcwIcon aria-hidden="true" />
        {restore.isPending ? "Restoring…" : "Restore"}
      </Button>
    </section>
  );
}
