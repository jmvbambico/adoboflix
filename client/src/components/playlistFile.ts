/**
 * @license
 * SPDX-License-Identifier: Apache-2.0
 */

import { useCallback, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  clearPlaylistFile,
  importPlaylistFile,
  isApiError,
  type SourceStatus,
} from "../api/client";
import { describeSourceError, type SourceStatusCopy } from "./sourceStatus";
import { SOURCE_STATUS_QUERY_KEY } from "./playlistCode";

export type PlaylistImportOutcome =
  | { kind: "imported" }
  | {
      kind: "failed";
      copy: SourceStatusCopy;
      // The server's own rejection message, kept verbatim. The import failure
      // names the parse cause and points at the documented format; paraphrasing
      // it would destroy the only information the user needs to fix the file,
      // so the modal renders this for invalid_playlist instead of the copy.
      serverMessage?: string;
    }
  | null;

// usePlaylistFileController owns the import mutation, the query invalidation
// that makes the swap visible, and the failure copy. It mirrors
// usePlaylistCodeController: a successful import has already swapped the server
// source, so the caches are invalidated — never a page reload, which would
// throw away the session for nothing.
export function usePlaylistFileController() {
  const queryClient = useQueryClient();
  const [outcome, setOutcome] = useState<PlaylistImportOutcome>(null);

  const submit = useMutation<SourceStatus, unknown, File>({
    mutationFn: async (file: File) => importPlaylistFile(await file.text()),
    onSuccess: (status) => {
      // The server swapped the source before answering, so its reply IS the new
      // status. Write it in and drop every cached answer so the library
      // refetches against the file source.
      queryClient.setQueryData<SourceStatus>(SOURCE_STATUS_QUERY_KEY, status);
      queryClient.invalidateQueries();
      setOutcome({ kind: "imported" });
    },
    onError: (error) => {
      setOutcome({
        kind: "failed",
        copy: describeSourceError(error),
        serverMessage: isApiError(error) ? error.message : undefined,
      });
    },
  });

  // importFile resets the retained File once the mutation settles, so the
  // chosen file does not linger in React Query state, and clears a stale
  // outcome when a new attempt starts.
  const importFile = useCallback(
    (file: File) => {
      setOutcome(null);
      submit.mutate(file, { onSettled: () => submit.reset() });
    },
    [submit],
  );

  // remove deletes the imported playlist. When it was the active source the
  // server returns to the sourceless state, so the reply reports active:false;
  // writing it in and invalidating drops the library and brings the chooser
  // back with no reload.
  const remove = useMutation<SourceStatus, unknown, void>({
    mutationFn: () => clearPlaylistFile(),
    onSuccess: (status) => {
      queryClient.setQueryData<SourceStatus>(SOURCE_STATUS_QUERY_KEY, status);
      queryClient.invalidateQueries();
    },
  });

  return {
    outcome,
    isPending: submit.isPending,
    importFile,
    remove,
    removeError: remove.isError ? describeSourceError(remove.error) : null,
  };
}
