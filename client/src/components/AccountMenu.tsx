/**
 * @license
 * SPDX-License-Identifier: Apache-2.0
 */

import { useCallback, useEffect, useRef, useState, type KeyboardEvent } from "react";
import { FileUp, KeyRound, LogOut, RefreshCw, User } from "lucide-react";
import { describeSourceError, sourceStatusCopy } from "./sourceStatus";
import { usePlaylistCodeController } from "./playlistCode";
import { activeSourceLabel, isPinned, selectableModes } from "./sourceModes";
import PlaylistCodeModal from "./PlaylistCodeModal";
import PlaylistImportModal from "./PlaylistImportModal";

type AccountModal = "code" | "import" | null;

// formatExpiry renders the RFC3339 billing expiry the server reported. An
// unparseable value is shown verbatim rather than as "Invalid Date".
function formatExpiry(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  return date.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
}

// AccountMenu is the real account control where a placeholder once sat: the
// avatar is a button that opens a dropdown describing the connected source and
// offering the two actions AdoboFlix actually has. AdoboFlix owns no accounts,
// so "log in" is connecting a playlist code and "log out" is disconnecting it —
// the only credential a subscriber has. Nothing here invents a tier, a plan
// label, or a display name: it shows only what the server reports.
//
// The offered actions are derived from the server's modes list, never from an
// adapter name. A development harness is labelled as one and never printed as a
// user's choice; an env-pinned source explains that its mode cannot change and
// disables the actions that would fail with 409.
export default function AccountMenu() {
  const [open, setOpen] = useState(false);
  const [confirmingDisconnect, setConfirmingDisconnect] = useState(false);
  const [accountModal, setAccountModal] = useState<AccountModal>(null);

  const buttonRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const confirmYesRef = useRef<HTMLButtonElement>(null);
  const disconnectRef = useRef<HTMLButtonElement>(null);
  const wasConfirming = useRef(false);

  const { statusQuery, clear } = usePlaylistCodeController();
  const status = statusQuery.data;
  const needsCode = Boolean(status?.needs_playlist_code);
  const configured = Boolean(status?.playlist_code_configured);
  const dev = Boolean(status?.dev);
  const pinned = isPinned(status);
  // Loading and failure are NOT "this source has no account concept". Until an
  // answer arrives we know nothing about whether a code is needed, so the menu
  // must not assert one.
  const statusResolved = status !== undefined;
  const statusFailed = statusQuery.isError && !statusResolved;

  const offers = selectableModes(status);
  const hasCodeOffer = offers.some((mode) => mode.needs_playlist_code);
  const hasFileOffer = offers.some((mode) => !mode.needs_playlist_code);
  const codeOffered = needsCode && configured ? "Change playlist code" : "Login to AdoboTV";

  const disconnectError = clear.isError ? describeSourceError(clear.error) : null;

  const closeMenu = useCallback((refocus: boolean) => {
    setOpen(false);
    setConfirmingDisconnect(false);
    if (refocus) buttonRef.current?.focus();
  }, []);

  // While the menu is open: move focus to its first action, close on a click
  // outside, and close on Escape (returning focus to the avatar).
  useEffect(() => {
    if (!open) return;
    menuRef.current?.querySelector<HTMLElement>('[role="menuitem"]')?.focus();

    const onPointerDown = (event: MouseEvent) => {
      const target = event.target as Node;
      if (menuRef.current?.contains(target) || buttonRef.current?.contains(target)) return;
      closeMenu(false);
    };
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.key === "Escape") {
        event.stopPropagation();
        closeMenu(true);
      }
    };
    document.addEventListener("mousedown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("mousedown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [open, closeMenu]);

  // Opening the disconnect confirmation unmounts the focused Disconnect button,
  // so focus must be moved into the confirmation; cancelling must move it back.
  // Without this, focus lands on <body> and the roving-focus arithmetic below
  // sees index -1.
  useEffect(() => {
    if (confirmingDisconnect) {
      confirmYesRef.current?.focus();
    } else if (wasConfirming.current) {
      const target =
        disconnectRef.current ?? menuRef.current?.querySelector<HTMLElement>('[role="menuitem"]');
      target?.focus();
    }
    wasConfirming.current = confirmingDisconnect;
  }, [confirmingDisconnect]);

  // Roving focus across the menu's actions, so role="menu" behaves as the role
  // promises for a keyboard user. An index of -1 (focus outside the items) is
  // treated as "start from an end" rather than arithmetically landing on the
  // second-to-last item.
  const handleMenuKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const items = Array.from(
      menuRef.current?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? [],
    );
    if (items.length === 0) return;
    const index = items.indexOf(document.activeElement as HTMLElement);
    if (event.key === "ArrowDown") {
      event.preventDefault();
      items[index === -1 ? 0 : (index + 1) % items.length]?.focus();
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      items[index === -1 ? items.length - 1 : (index - 1 + items.length) % items.length]?.focus();
    } else if (event.key === "Home") {
      event.preventDefault();
      items[0]?.focus();
    } else if (event.key === "End") {
      event.preventDefault();
      items[items.length - 1]?.focus();
    }
  };

  const openModal = (kind: Exclude<AccountModal, null>) => {
    setAccountModal(kind);
    closeMenu(false);
  };

  // Return focus to the avatar when a modal closes, so it does not fall to
  // <body> as the focused input unmounts.
  const closeModal = useCallback(() => {
    setAccountModal(null);
    buttonRef.current?.focus();
  }, []);

  return (
    <div className="relative">
      <button
        ref={buttonRef}
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? "account-menu" : undefined}
        aria-label="Account menu"
        onClick={() => setOpen((prev) => !prev)}
        onKeyDown={(event) => {
          if (event.key === "ArrowDown") {
            event.preventDefault();
            setOpen(true);
          }
        }}
        className="p-0.5 rounded-xl cursor-pointer focus:outline-none focus:ring-1 focus:ring-orange-500/50 transition-all"
      >
        <span className="w-9 h-9 rounded-xl bg-slate-800 border border-white/10 overflow-hidden flex items-center justify-center text-slate-300 shadow bg-gradient-to-tr from-orange-600/30 to-indigo-500/30">
          <User className="w-4 h-4" />
        </span>
      </button>

      {open && (
        <div
          id="account-menu"
          ref={menuRef}
          role="menu"
          aria-label="Account menu"
          onKeyDown={handleMenuKeyDown}
          className="absolute right-0 mt-2 w-72 z-50 glass-panel rounded-2xl border border-white/10 p-3 flex flex-col gap-3 shadow-2xl"
        >
          <div className="flex flex-col gap-1 px-1" role="none">
            <span className="text-[10px] font-mono uppercase tracking-widest text-slate-500">
              Active source
            </span>
            <span className="text-sm font-semibold text-slate-100">
              {statusResolved ? activeSourceLabel(status) : statusFailed ? "—" : "…"}
            </span>
            {!statusResolved ? (
              statusFailed ? (
                <span className="text-[11px] leading-relaxed text-red-300">
                  Can't read the source status.
                </span>
              ) : (
                <span className="text-[11px] leading-relaxed text-slate-400">
                  Checking the active source…
                </span>
              )
            ) : dev ? null : needsCode ? (
              <span
                className={`text-[11px] font-medium ${
                  configured ? "text-emerald-400" : "text-amber-300"
                }`}
              >
                {configured ? "Playlist code connected" : "No playlist code yet"}
              </span>
            ) : (
              <span className="text-[11px] leading-relaxed text-slate-400">
                No account needed — this source reads a local playlist.
              </span>
            )}
            {statusResolved && status?.subscription_expires_at && (
              <span className="text-[11px] text-slate-300">
                Subscription renews {formatExpiry(status.subscription_expires_at)}
              </span>
            )}
            {statusResolved && status?.user_message && (
              <span className="text-[11px] leading-relaxed text-slate-300">{status.user_message}</span>
            )}
          </div>

          {statusFailed && (
            <>
              <div className="h-px bg-white/5" role="none" />
              <button
                type="button"
                role="menuitem"
                onClick={() => {
                  void statusQuery.refetch();
                }}
                className="px-3 py-2 rounded-lg border border-white/10 text-slate-200 hover:bg-white/5 text-[11px] font-semibold flex items-center gap-2 transition-all cursor-pointer focus:outline-none"
              >
                <RefreshCw className="w-3.5 h-3.5" />
                Retry
              </button>
            </>
          )}

          {statusResolved && (
            <>
              <div className="h-px bg-white/5" role="none" />

              {pinned && (
                <p className="px-1 text-[11px] leading-relaxed text-amber-300/90">
                  {sourceStatusCopy("source_pinned_by_env").message}
                </p>
              )}

              {hasCodeOffer && (
                <button
                  type="button"
                  role="menuitem"
                  disabled={pinned}
                  onClick={() => openModal("code")}
                  className="px-3 py-2 rounded-lg border border-white/10 text-slate-200 hover:bg-white/5 text-[11px] font-semibold flex items-center gap-2 transition-all cursor-pointer focus:outline-none disabled:opacity-50 disabled:cursor-not-allowed"
                >
                  <KeyRound className="w-3.5 h-3.5" />
                  {codeOffered}
                </button>
              )}

              {hasFileOffer && (
                <button
                  type="button"
                  role="menuitem"
                  disabled={pinned}
                  onClick={() => openModal("import")}
                  className="px-3 py-2 rounded-lg border border-white/10 text-slate-200 hover:bg-white/5 text-[11px] font-semibold flex items-center gap-2 transition-all cursor-pointer focus:outline-none disabled:opacity-50 disabled:cursor-not-allowed"
                >
                  <FileUp className="w-3.5 h-3.5" />
                  Import Local Playlist
                </button>
              )}

              {needsCode && configured && (
                <>
                  {confirmingDisconnect ? (
                    <div className="flex flex-col gap-2" role="none">
                      <p className="text-[11px] leading-relaxed text-slate-300">
                        Disconnecting removes the saved playlist code from this server. You will need
                        to type it in again in full.
                      </p>
                      <button
                        ref={confirmYesRef}
                        type="button"
                        role="menuitem"
                        onClick={() => {
                          setConfirmingDisconnect(false);
                          clear.mutate();
                        }}
                        disabled={clear.isPending}
                        className="px-3 py-2 rounded-lg bg-red-500/10 border border-red-500/20 text-red-300 hover:bg-red-500/20 text-[11px] font-semibold text-left transition-all cursor-pointer focus:outline-none disabled:opacity-50"
                      >
                        {clear.isPending ? "Disconnecting…" : "Yes, disconnect"}
                      </button>
                      <button
                        type="button"
                        role="menuitem"
                        onClick={() => setConfirmingDisconnect(false)}
                        className="px-3 py-2 rounded-lg border border-white/10 text-slate-300 hover:bg-white/5 text-[11px] font-semibold text-left transition-all cursor-pointer focus:outline-none"
                      >
                        Cancel
                      </button>
                    </div>
                  ) : (
                    <button
                      ref={disconnectRef}
                      type="button"
                      role="menuitem"
                      onClick={() => setConfirmingDisconnect(true)}
                      className="px-3 py-2 rounded-lg border border-transparent text-slate-300 hover:text-red-300 hover:border-red-500/30 hover:bg-red-500/10 text-[11px] font-semibold flex items-center gap-2 transition-all cursor-pointer focus:outline-none"
                    >
                      <LogOut className="w-3.5 h-3.5" />
                      Disconnect
                    </button>
                  )}
                  {disconnectError && (
                    <p role="alert" className="px-1 text-[11px] leading-relaxed text-red-300">
                      {disconnectError.message}
                    </p>
                  )}
                </>
              )}
            </>
          )}
        </div>
      )}

      {accountModal === "code" && (
        <PlaylistCodeModal configured={configured} onClose={closeModal} />
      )}
      {accountModal === "import" && <PlaylistImportModal onClose={closeModal} />}
    </div>
  );
}
