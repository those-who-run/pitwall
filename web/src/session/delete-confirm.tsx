// The confirm before a delete (ADR 0014 in paddock), naming the session by its
// track, car and date. A chunk of its own, asked for only when Delete is
// pressed, so the dialog never weighs on the session page's first load.
import { useDeleteSession, type Session } from "@/api/hooks";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

import { failure, sessionName } from "./deleting";

export function DeleteConfirm({
  session: s,
  onClose,
  onDeleted,
}: {
  session: Session;
  onClose: () => void;
  onDeleted?: () => void;
}) {
  const del = useDeleteSession(s.session_id, onDeleted);
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete this session?</DialogTitle>
          <DialogDescription data-slot="delete-name" className="font-semibold text-fg">
            {sessionName(s)}
          </DialogDescription>
        </DialogHeader>
        <p className="text-sm text-muted">
          It leaves the session list and lap compare. Nothing is thrown away: its page still opens, with Restore.
        </p>
        {del.error && (
          <p role="alert" className="text-sm">
            {failure(del.error, "delete")}
          </p>
        )}
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline">Cancel</Button>
          </DialogClose>
          <Button disabled={del.isPending} onClick={() => del.mutate(undefined, { onSuccess: onClose })}>
            {del.isPending ? "Deleting…" : "Delete"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
