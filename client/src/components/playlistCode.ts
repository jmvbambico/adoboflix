/**
 * @license
 * SPDX-License-Identifier: Apache-2.0
 */

import { useCallback, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  clearPlaylistCode,
  fetchSourceStatus,
  isApiError,
  setPlaylistCode,
  type SourceStatus,
} from "../api/client";
import { describeSourceError, type SourceStatusCopy } from "./sourceStatus";

// The playlist-code credential is submitted and cleared from two places: the
// inline gate in the page body and the account menu's modal. Both must branch
// on the same server behaviour — which gate codes mean the code was kept, and
// what a saved-but-gated connect should say — so that logic lives here in one
// controller rather than being forked. The gate and the modal each mount it;
// the underlying status query is shared through React Query's cache.
export const SOURCE_STATUS_QUERY_KEY = ["source-status"] as const;

// Codes for which the server kept the submitted code: it proved the code itself
// valid and only a gate outside the code's control remains. This mirrors
// playlistCodeProvenValid in internal/handler/source_control.go — the two must
// stay in step, or the form would tell a user to re-enter a code the server
// already saved.
const SAVED_GATE_CODES = new Set([
  "device_pending",
  "subscription_inactive",
  "playlist_format_m3u",
  "content_token_rejected",
  "content_not_found",
]);

// A saved code is not a failure. Say plainly that nothing needs re-entering and
// why playback has not started yet, so a first connect does not send the user
// back to retype a code that is already on the server.
const SAVED_REASSURANCE: Record<string, string> = {
  device_pending:
    "AdoboTV accepted this code and AdoboFlix saved it on the server. Nothing needs re-entering — playback starts on its own once the AdoboTV operator approves this device.",
  subscription_inactive:
    "AdoboTV accepted this code and AdoboFlix saved it on the server. Nothing needs re-entering — playback starts on its own once your subscription is active again.",
};
const SAVED_REASSURANCE_DEFAULT =
  "AdoboTV accepted this code and AdoboFlix saved it on the server. Nothing needs re-entering.";

// savedReassurance returns the reassurance copy for a saved-but-gated connect,
// falling back to the generic wording for any other kept-code gate.
export function savedReassurance(code?: string): string {
  return SAVED_REASSURANCE[code ?? ""] ?? SAVED_REASSURANCE_DEFAULT;
}

export type PlaylistCodeOutcome =
  | { kind: "saved"; copy: SourceStatusCopy }
  | { kind: "failed"; copy: SourceStatusCopy }
  | null;

// usePlaylistCodeController owns the status query and the submit/clear
// mutations, including the subtle "the server kept the code" branch and the
// optimistic status-cache updates that keep a remount from asking for a code
// the server already saved.
export function usePlaylistCodeController() {
  const queryClient = useQueryClient();
  const [outcome, setOutcome] = useState<PlaylistCodeOutcome>(null);

  const statusQuery = useQuery<SourceStatus>({
    queryKey: SOURCE_STATUS_QUERY_KEY,
    queryFn: fetchSourceStatus,
  });

  const submit = useMutation<SourceStatus, unknown, string>({
    mutationFn: (value: string) => setPlaylistCode(value),
    onSuccess: () => {
      // The server already validated the code and swapped the adapter. Drop
      // every cached answer so the library refetches against the new source;
      // a page reload would throw away the session for nothing.
      queryClient.setQueryData<SourceStatus>(SOURCE_STATUS_QUERY_KEY, (prev) =>
        prev ? { ...prev, playlist_code_configured: true } : prev,
      );
      queryClient.invalidateQueries();
      setOutcome(null);
    },
    onError: (error) => {
      // A gate that proves the code valid leaves it persisted; the rest persist
      // nothing and the user must correct the code.
      if (isApiError(error) && error.code && SAVED_GATE_CODES.has(error.code)) {
        // The server kept the code, so record that in the status cache too.
        // Without this the cache still says "not configured", and a remount
        // inside the stale window would ask for a code that is already saved —
        // exactly the retype this feature exists to prevent. Invalidate the
        // status key as well so a fresh read replaces the optimistic one.
        queryClient.setQueryData<SourceStatus>(SOURCE_STATUS_QUERY_KEY, (prev) =>
          prev ? { ...prev, playlist_code_configured: true } : prev,
        );
        queryClient.invalidateQueries({ queryKey: SOURCE_STATUS_QUERY_KEY });
        setOutcome({ kind: "saved", copy: describeSourceError(error) });
        return;
      }
      setOutcome({ kind: "failed", copy: describeSourceError(error) });
    },
  });

  const clear = useMutation<SourceStatus, unknown, void>({
    mutationFn: () => clearPlaylistCode(),
    onSuccess: () => {
      queryClient.setQueryData<SourceStatus>(SOURCE_STATUS_QUERY_KEY, (prev) =>
        prev ? { ...prev, playlist_code_configured: false } : prev,
      );
      queryClient.invalidateQueries();
      setOutcome(null);
    },
  });

  // submitCode clears the mutation's retained variables once it settles, so the
  // entered code is not kept in React Query state after it has been sent, and
  // fires onConnected only on a fully successful connect (a saved-but-gated
  // connect reports its outcome instead and stays open).
  const submitCode = useCallback(
    (value: string, onConnected?: () => void) => {
      submit.mutate(value, {
        onSuccess: () => onConnected?.(),
        onSettled: () => submit.reset(),
      });
    },
    [submit],
  );

  return { statusQuery, submit, clear, submitCode, outcome, setOutcome };
}
