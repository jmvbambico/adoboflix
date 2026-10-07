/**
 * @license
 * SPDX-License-Identifier: Apache-2.0
 */

import { useState, type FormEvent, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, KeyRound, LoaderCircle, RefreshCw } from "lucide-react";
import { describeSourceError, sourceStatusCopy } from "./sourceStatus";
import { savedReassurance, usePlaylistCodeController } from "./playlistCode";
import SourceChooser from "./SourceChooser";
import SourceStatusPanel from "./SourceStatusPanel";
import PlaylistImportModal from "./PlaylistImportModal";

interface PlaylistCodeGateProps {
  // Errors from requests the caller already issued. Any carrying
  // playlist_code_required shows the entry surface even before, or without, the
  // status query settling — the server tells the client it needs a credential
  // the same way it tells it a stream failed.
  errors?: unknown[];
}

// How the login path collects the subscriber's credential. Username/password is
// the entry method; a playlist code stays reachable as a secondary option for a
// user who only has a code and no AdoboTV account.
type ConnectMode = "credentials" | "code";

// PlaylistCodeGate is the page-body source surface. When no source is active it
// becomes the first-run chooser: the two ways to connect are the primary
// content, not chrome above an empty library. Choosing "Login to AdoboTV" opens
// the username/password form in place; "Import Local Playlist" opens the import
// modal. Once a source is active the chooser is gone and the login surface —
// while a credential is still needed — remains.
//
// The password is the subscriber's most sensitive credential, so it is handled
// more carefully than the code: the field is masked, never rendered back, never
// logged, and cleared from the DOM the moment the form is submitted. The server
// — not this component — talks to AdoboTV, and it stores only the playlist code
// it reads from the account. The submit/gate-code handling lives in
// usePlaylistCodeController, shared with the account menu's modal.
//
// Three status states are handled honestly: a failed read shows the error and a
// Retry rather than a chooser built on absent data, and a pending read shows
// nothing rather than guessing whether a source is connected.
export default function PlaylistCodeGate({ errors = [] }: PlaylistCodeGateProps) {
  const queryClient = useQueryClient();
  const [mode, setMode] = useState<ConnectMode>("credentials");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [loginRequested, setLoginRequested] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const { statusQuery, submit, login, submitCode, submitLogin, outcome } = usePlaylistCodeController();

  const status = statusQuery.data;
  const active = Boolean(status?.active);
  const needsCode = Boolean(status?.needs_playlist_code);
  const configured = Boolean(status?.playlist_code_configured);
  const requestNeedsEntry = errors.some(
    (error) => describeSourceError(error).code === "playlist_code_required",
  );

  const saved = outcome?.kind === "saved";
  const failed = outcome?.kind === "failed" ? outcome.copy : null;
  // Defer to server truth: once the status says a code is configured, do not
  // keep forcing the form just because a request once failed or an old error
  // still asks for a code.
  const showForm =
    !saved && !configured && (needsCode || requestNeedsEntry || failed !== null || loginRequested);

  const handleCredentialSubmit = (event: FormEvent) => {
    event.preventDefault();
    const user = username.trim();
    if (!user || !password) return;
    const secret = password;
    // Clear the password before the request settles: it must not stay in the
    // mounted DOM, and on failure the user retypes against the error copy.
    setPassword("");
    submitLogin(user, secret);
  };

  const handleCodeSubmit = (event: FormEvent) => {
    event.preventDefault();
    const value = code.trim();
    if (!value) return;
    setCode("");
    submitCode(value);
  };

  // The heading and body copy for the mode in play. A failure's own copy wins in
  // either mode, so a refused login says what it should rather than a generic
  // prompt.
  const credentialIntro = {
    title: "Login to AdoboTV",
    message:
      "Sign in with your AdoboTV username and password. AdoboFlix stores only the playlist code it reads from your account — never your password.",
    hint: "You can do this right now — no restart or reinstall needed.",
  };
  const codeIntro = sourceStatusCopy("playlist_code_required");
  const intro = failed ?? (mode === "credentials" ? credentialIntro : codeIntro);

  let content: ReactNode = null;
  if (!status) {
    // Failed read: show the error and Retry, never a chooser built on absent
    // data. Pending read: show nothing rather than guess.
    content = statusQuery.isError ? (
      <SourceStatusPanel
        error={statusQuery.error}
        onRetry={() => {
          void statusQuery.refetch();
        }}
      />
    ) : null;
  } else if (!saved && !active && !loginRequested) {
    content = (
      <SourceChooser
        status={status}
        onLogin={() => setLoginRequested(true)}
        onImport={() => setImportOpen(true)}
      />
    );
  } else if (saved || showForm) {
    content = (
      <section
        aria-label="AdoboTV source connection"
        className="glass-panel rounded-3xl border border-white/5 p-5 sm:p-6 flex flex-col gap-4"
      >
        <div className="flex items-start gap-4">
          <div className="p-3 rounded-2xl border shrink-0 text-amber-300 bg-amber-500/10 border-amber-500/20">
            <KeyRound className="w-5 h-5" />
          </div>
          <div className="flex flex-col gap-1.5 min-w-0">
            {saved ? (
              <>
                <h4 className="font-display font-bold text-base sm:text-lg text-emerald-300 tracking-wide">
                  Playlist code saved
                </h4>
                <p className="text-xs sm:text-sm text-slate-300 leading-relaxed max-w-2xl">
                  {savedReassurance(outcome.copy.code)}
                </p>
                <p className="text-xs text-slate-400 leading-relaxed max-w-2xl">
                  {outcome.copy.message}
                </p>
              </>
            ) : (
              <>
                <h4 className="font-display font-bold text-base sm:text-lg text-slate-100 tracking-wide">
                  {intro.title}
                </h4>
                <p className="text-xs sm:text-sm text-slate-400 leading-relaxed max-w-2xl">
                  {intro.message}
                </p>
                <p className="text-xs leading-relaxed max-w-2xl font-medium text-amber-300/90">
                  {intro.hint}
                </p>
              </>
            )}
          </div>
        </div>

        {showForm && mode === "credentials" && (
          <form onSubmit={handleCredentialSubmit} className="flex flex-col gap-3">
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div className="flex flex-col gap-1.5 min-w-0">
                <label htmlFor="adobotv-username" className="text-[11px] font-semibold text-slate-300">
                  AdoboTV username
                </label>
                <input
                  id="adobotv-username"
                  name="username"
                  type="text"
                  value={username}
                  onChange={(event) => setUsername(event.target.value)}
                  placeholder="Your AdoboTV username"
                  autoComplete="off"
                  autoFocus={failed === null}
                  disabled={login.isPending}
                  className="w-full px-3 py-2.5 rounded-xl bg-slate-950/50 border border-white/10 text-sm text-slate-100 placeholder:text-slate-600 focus:outline-none focus:border-orange-500/40 focus:ring-1 focus:ring-orange-500/20 disabled:opacity-60"
                />
              </div>
              <div className="flex flex-col gap-1.5 min-w-0">
                <label htmlFor="adobotv-password" className="text-[11px] font-semibold text-slate-300">
                  AdoboTV password
                </label>
                <input
                  id="adobotv-password"
                  name="password"
                  type="password"
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                  placeholder="Your AdoboTV password"
                  autoComplete="off"
                  disabled={login.isPending}
                  className="w-full px-3 py-2.5 rounded-xl bg-slate-950/50 border border-white/10 text-sm text-slate-100 placeholder:text-slate-600 focus:outline-none focus:border-orange-500/40 focus:ring-1 focus:ring-orange-500/20 disabled:opacity-60"
                />
              </div>
            </div>
            <div className="flex flex-wrap items-center gap-3">
              <button
                type="submit"
                disabled={login.isPending || username.trim() === "" || password === ""}
                className="shrink-0 px-5 py-2.5 bg-gradient-to-tr from-orange-600 to-amber-500 hover:from-orange-700 hover:to-amber-600 rounded-xl text-xs font-bold tracking-wider text-white shadow-lg shadow-orange-600/20 flex items-center justify-center gap-2 transition-all active:scale-98 cursor-pointer focus:outline-none disabled:opacity-50 disabled:cursor-not-allowed"
              >
                {login.isPending ? (
                  <LoaderCircle className="w-3.5 h-3.5 animate-spin" />
                ) : (
                  <KeyRound className="w-3.5 h-3.5" />
                )}
                {login.isPending ? "Connecting…" : "Connect"}
              </button>
              {!active && (
                <button
                  type="button"
                  onClick={() => setLoginRequested(false)}
                  className="shrink-0 px-4 py-2.5 bg-white/5 border border-white/10 hover:bg-white/10 rounded-xl text-xs font-bold tracking-wider text-slate-300 flex items-center gap-2 transition-all cursor-pointer focus:outline-none"
                >
                  <ArrowLeft className="w-3.5 h-3.5" />
                  Back
                </button>
              )}
            </div>
          </form>
        )}

        {showForm && mode === "code" && (
          <form
            onSubmit={handleCodeSubmit}
            className="flex flex-col sm:flex-row sm:items-end gap-3"
          >
            <div className="flex flex-col gap-1.5 flex-1 min-w-0">
              <label htmlFor="playlist-code-input" className="text-[11px] font-semibold text-slate-300">
                Playlist code
              </label>
              <input
                id="playlist-code-input"
                name="playlist-code"
                type="password"
                value={code}
                onChange={(event) => setCode(event.target.value)}
                placeholder="Enter your AdoboTV playlist code"
                autoComplete="off"
                autoFocus={failed === null}
                disabled={submit.isPending}
                className="w-full px-3 py-2.5 rounded-xl bg-slate-950/50 border border-white/10 text-sm text-slate-100 placeholder:text-slate-600 focus:outline-none focus:border-orange-500/40 focus:ring-1 focus:ring-orange-500/20 disabled:opacity-60"
              />
            </div>
            <button
              type="submit"
              disabled={submit.isPending || code.trim() === ""}
              className="shrink-0 px-5 py-2.5 bg-gradient-to-tr from-orange-600 to-amber-500 hover:from-orange-700 hover:to-amber-600 rounded-xl text-xs font-bold tracking-wider text-white shadow-lg shadow-orange-600/20 flex items-center justify-center gap-2 transition-all active:scale-98 cursor-pointer focus:outline-none disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {submit.isPending ? (
                <LoaderCircle className="w-3.5 h-3.5 animate-spin" />
              ) : (
                <KeyRound className="w-3.5 h-3.5" />
              )}
              {submit.isPending ? "Connecting…" : "Connect"}
            </button>
            {!active && (
              <button
                type="button"
                onClick={() => setLoginRequested(false)}
                className="shrink-0 px-4 py-2.5 bg-white/5 border border-white/10 hover:bg-white/10 rounded-xl text-xs font-bold tracking-wider text-slate-300 flex items-center gap-2 transition-all cursor-pointer focus:outline-none"
              >
                <ArrowLeft className="w-3.5 h-3.5" />
                Back
              </button>
            )}
          </form>
        )}

        {showForm && (
          <button
            type="button"
            onClick={() => {
              setMode((prev) => (prev === "credentials" ? "code" : "credentials"));
            }}
            className="w-fit text-xs font-semibold text-orange-300/90 hover:text-orange-200 underline underline-offset-4 cursor-pointer focus:outline-none"
          >
            {mode === "credentials"
              ? "Only have a playlist code? Use it instead"
              : "Use your AdoboTV username and password instead"}
          </button>
        )}

        {saved && (
          <button
            type="button"
            onClick={() => queryClient.invalidateQueries()}
            className="w-fit px-5 py-2.5 bg-white/5 border border-white/10 hover:bg-white/10 rounded-xl text-xs font-bold tracking-wider text-slate-200 flex items-center gap-2 transition-all cursor-pointer focus:outline-none"
          >
            <RefreshCw className="w-3.5 h-3.5" />
            Check again
          </button>
        )}
      </section>
    );
  }

  return (
    <>
      {content}
      {importOpen && <PlaylistImportModal onClose={() => setImportOpen(false)} />}
    </>
  );
}
