// The words of deleting and restoring a session (ADR 0014 in paddock), for the
// session page's Delete, its confirm and the deleted note, and where the focus
// goes after either.
import { useEffect, type RefObject } from "react";

import { ApiError, type Session } from "@/api/hooks";
import { sessionDate } from "@/lib/format";

/** The session as the confirm names it: "Suzuka Circuit East Course · Honda
 * Civic … · Today 14:21". */
export function sessionName(s: Session): string {
  return [s.track.name, s.car ?? "Unknown car", sessionDate(s.started_at)].join(" · ");
}

/** What went wrong with a delete or a restore, and what to do. */
export function failure(error: Error, what: "delete" | "restore"): string {
  if (error instanceof ApiError && error.status === 409) {
    return "The session is still open, so it can't be deleted yet. Try again once it has ended.";
  }
  if (error instanceof ApiError && error.status === 404) {
    return "The pit wall has no such session any more.";
  }
  return `Couldn't ${what} the session: ${error.message}. Try again, or check the engineer's logs.`;
}

/** Focuses `ref` once `on` is true: after the dialog's own focus handling,
 * which runs on a timer as it closes. */
export function useFocus(ref: RefObject<HTMLElement | null>, on: boolean) {
  useEffect(() => {
    if (!on) {
      return;
    }
    const timer = setTimeout(() => ref.current?.focus(), 0);
    return () => clearTimeout(timer);
  }, [ref, on]);
}
