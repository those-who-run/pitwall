// Deleting a session from its page, and bringing it back (ADR 0014 in paddock).
// A delete is soft: logbook keeps everything, the session list and lap compare
// leave the session out, and its page still opens, saying so, with Restore. So
// the delete asks once, naming the session (track, car and date), and the
// restore doesn't ask.
import { RotateCcwIcon, Trash2Icon } from "lucide-react";
import { lazy, Suspense, useState } from "react";

import { useRestoreSession, type Session } from "@/api/hooks";
import { Button } from "@/components/ui/button";
import { sessionDate } from "@/lib/format";

import { failure } from "./deleting";

// The confirm carries the dialog: a chunk of its own, asked for on a press.
const DeleteConfirm = lazy(() => import("./delete-confirm").then((m) => ({ default: m.DeleteConfirm })));

/** The page's Delete button, which opens the confirm. Only for a closed session
 * that isn't deleted: the page leaves it out otherwise. */
export function DeleteSession({ session: s }: { session: Session }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button variant="outline" aria-haspopup="dialog" onClick={() => setOpen(true)}>
        <Trash2Icon aria-hidden="true" />
        Delete session
      </Button>
      {open && (
        <Suspense fallback={null}>
          <DeleteConfirm session={s} onClose={() => setOpen(false)} />
        </Suspense>
      )}
    </>
  );
}

/** The note a deleted session's page leads with, and Restore. */
export function DeletedNote({ session: s }: { session: Session }) {
  const restore = useRestoreSession(s.session_id);
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
      <Button variant="outline" disabled={restore.isPending} onClick={() => restore.mutate()}>
        <RotateCcwIcon aria-hidden="true" />
        {restore.isPending ? "Restoring…" : "Restore"}
      </Button>
    </section>
  );
}
