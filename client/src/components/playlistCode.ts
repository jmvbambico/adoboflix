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
  loginToAdoboTV,
  setPlaylistCode,
  syncSource,
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

// useSourceStatus is the one status read. The gate, the account menu and the
// dashboard's decision to show the library or the chooser all mount it; React
// Query's shared cache means the server is asked once. It is separate from the
// controller so a caller that only needs to read status — the dashboard — does
// not also build the submit and clear mutations.
export function useSourceStatus() {
  return useQuery<SourceStatus>({
    queryKey: SOURCE_STATUS_QUERY_KEY,
    queryFn: fetchSourceStatus,
  });
}

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

// AdoboTVCredentials is the username/password a user signs in with. The password
// is held only long enough to send it: the controller resets the mutation the
// moment it settles, so the value does not linger in React Query state.
export interface AdoboTVCredentials {
  username: string;
  password: string;
}

// usePlaylistCodeController owns the status query and the connect mutations —
// submitting a playlist code, or logging in with a username and password — plus
// the subtle "the server kept the credential" branch and the optimistic
// status-cache updates that keep a remount from asking for a credential the
// server already saved.
//
// Both connect paths share the same success and failure handling because both
// end in a playlist code: logging in stores the code the profile carried, so a
// login is a playlist-code connect the user did not have to type. The failure
// vocabulary is therefore shared too — a saved-but-gated login recovers exactly
// as a saved-but-gated typed code does.
export function usePlaylistCodeController() {
  const queryClient = useQueryClient();
  const [outcome, setOutcome] = useState<PlaylistCodeOutcome>(null);

  const statusQuery = useSourceStatus();

  const handleConnected = useCallback(() => {
    // The server already validated the credential and swapped the adapter. Drop
    // every cached answer so the library refetches against the new source; a
    // page reload would throw away the session for nothing.
    queryClient.setQueryData<SourceStatus>(SOURCE_STATUS_QUERY_KEY, (prev) =>
      prev ? { ...prev, playlist_code_configured: true } : prev,
    );
    queryClient.invalidateQueries();
    setOutcome(null);
  }, [queryClient]);

  const onConnectFailed = useCallback(
    (error: unknown) => {
      // A gate that proves the credential valid leaves it persisted; the rest
      // persist nothing and the user must correct it.
      if (isApiError(error) && error.code && SAVED_GATE_CODES.has(error.code)) {
        // The server kept the credential, so record that in the status cache
        // too. Without this the cache still says "not configured", and a remount
        // inside the stale window would ask for a credential that is already
        // saved — exactly the retype this feature exists to prevent. Invalidate
        // the status key as well so a fresh read replaces the optimistic one.
        queryClient.setQueryData<SourceStatus>(SOURCE_STATUS_QUERY_KEY, (prev) =>
          prev ? { ...prev, playlist_code_configured: true } : prev,
        );
        queryClient.invalidateQueries({ queryKey: SOURCE_STATUS_QUERY_KEY });
        setOutcome({ kind: "saved", copy: describeSourceError(error) });
        return;
      }
      setOutcome({ kind: "failed", copy: describeSourceError(error) });
    },
    [queryClient],
  );

  const submit = useMutation<SourceStatus, unknown, string>({
    mutationFn: (value: string) => setPlaylistCode(value),
    onSuccess: handleConnected,
    onError: onConnectFailed,
  });

  const login = useMutation<SourceStatus, unknown, AdoboTVCredentials>({
    mutationFn: ({ username, password }) => loginToAdoboTV(username, password),
    onSuccess: handleConnected,
    onError: onConnectFailed,
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

  // submitLogin does the same for the password: resetting on settle drops the
  // credentials from React Query's retained mutation state the instant the
  // request finishes, so the password survives only for the request itself.
  const submitLogin = useCallback(
    (username: string, password: string, onConnected?: () => void) => {
      login.mutate(
        { username, password },
        {
          onSuccess: () => onConnected?.(),
          onSettled: () => login.reset(),
        },
      );
    },
    [login],
  );

  return { statusQuery, submit, login, clear, submitCode, submitLogin, outcome, setOutcome };
}

// useSourceSync owns the manual "Sync now" action. It is separate from the
// playlist-code controller because it is only meaningful for an upstream-backed
// source, and the account menu mounts it only where the server reports that
// source. The reply is the new status, so it is written straight into the
// shared cache and the last-synced line updates without a second read.
export function useSourceSync() {
  const queryClient = useQueryClient();
  const sync = useMutation<SourceStatus, unknown, void>({
    mutationFn: syncSource,
    onSuccess: (status) => {
      queryClient.setQueryData<SourceStatus>(SOURCE_STATUS_QUERY_KEY, status);
    },
  });
  return { sync, syncError: sync.isError ? describeSourceError(sync.error) : null };
}
