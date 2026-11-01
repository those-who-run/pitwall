// Deleting a session from its page, and bringing it back (ADR 0014 in paddock).
// A delete is soft: logbook keeps everything, the session list and lap compare
// leave the session out, and its page still opens, saying so, with Restore. So
// the delete asks once, naming the session (track, car and date), and the
// restore doesn't ask.
import { Trash2Icon } from "lucide-react";
import { lazy, Suspense, useRef, useState } from "react";

import type { Session } from "@/api/hooks";
import { Button } from "@/components/ui/button";

import { useFocus } from "./deleting";

// The confirm carries the dialog: a chunk of its own, asked for on a press. So
// is the deleted note, which only a deleted session's page shows.
const DeletedNoteChunk = lazy(() => import("./deleted-note").then((m) => ({ default: m.DeletedNote })));
const DeleteConfirm = lazy(() => import("./delete-confirm").then((m) => ({ default: m.DeleteConfirm })));

/** The page's Delete button, which opens the confirm. Only for a closed session
 * that isn't deleted: the page leaves it out otherwise. `focus` takes the
 * focus to it, as after a restore; `onDeleted` is told of a delete. */
export function DeleteSession({
  session: s,
  focus = false,
  onDeleted,
}: {
  session: Session;
  focus?: boolean;
  onDeleted?: () => void;
}) {
  const [open, setOpen] = useState(false);
  const button = useRef<HTMLButtonElement>(null);
  useFocus(button, focus);
  return (
    <>
      <Button ref={button} variant="outline" aria-haspopup="dialog" onClick={() => setOpen(true)}>
        <Trash2Icon aria-hidden="true" />
        Delete session
      </Button>
      {open && (
        <Suspense fallback={null}>
          <DeleteConfirm session={s} onClose={() => setOpen(false)} onDeleted={onDeleted} />
        </Suspense>
      )}
    </>
  );
}

/** The note a deleted session's page leads with, and Restore (./deleted-note). */
export function DeletedNote(props: Parameters<typeof DeletedNoteChunk>[0]) {
  return (
    <Suspense fallback={null}>
      <DeletedNoteChunk {...props} />
    </Suspense>
  );
}
