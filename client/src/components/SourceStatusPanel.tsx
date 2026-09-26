/**
 * @license
 * SPDX-License-Identifier: Apache-2.0
 */

import { Clock, RefreshCw, SearchX, ServerCrash, ShieldAlert, WifiOff } from "lucide-react";
import { describeSourceError, SourceStatusSeverity } from "./sourceStatus";

interface SourceStatusPanelProps {
  error: unknown;
  onRetry?: () => void;
  className?: string;
}

const ICONS: Record<SourceStatusSeverity, typeof Clock> = {
  action: Clock,
  blocked: ShieldAlert,
  upstream: ServerCrash,
  notfound: SearchX,
  error: WifiOff,
};

const TONES: Record<SourceStatusSeverity, { icon: string; hint: string }> = {
  action: { icon: "text-amber-300 bg-amber-500/10 border-amber-500/20", hint: "text-amber-300/90" },
  blocked: { icon: "text-red-400 bg-red-500/10 border-red-500/20", hint: "text-red-300/90" },
  upstream: { icon: "text-cyan-300 bg-cyan-500/10 border-cyan-500/20", hint: "text-cyan-300/90" },
  notfound: { icon: "text-slate-300 bg-white/5 border-white/10", hint: "text-slate-400" },
  error: { icon: "text-rose-300 bg-rose-500/10 border-rose-500/20", hint: "text-rose-300/90" },
};

// One panel for every failure code: it renders only the mapped copy from
// sourceStatus, never the raw error message or response body.
export default function SourceStatusPanel({ error, onRetry, className }: SourceStatusPanelProps) {
  const copy = describeSourceError(error);
  const Icon = ICONS[copy.severity];
  const tone = TONES[copy.severity];

  return (
    <div className={`glass-panel rounded-3xl border border-white/5 p-6 sm:p-8 flex flex-col gap-5 ${className ?? ""}`}>
      <div className="flex items-start gap-4">
        <div className={`p-3 rounded-2xl border shrink-0 ${tone.icon}`}>
          <Icon className="w-6 h-6" />
        </div>
        <div className="flex flex-col gap-1.5 min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <h4 className="font-display font-bold text-base sm:text-lg text-slate-100 tracking-wide">{copy.title}</h4>
            {copy.code && (
              <span className="text-[9px] font-mono text-slate-400 bg-white/5 border border-white/10 px-1.5 py-0.5 rounded">
                {copy.code}
              </span>
            )}
          </div>
          <p className="text-xs sm:text-sm text-slate-400 leading-relaxed max-w-2xl">{copy.message}</p>
          {copy.hint && (
            <p className={`text-xs leading-relaxed max-w-2xl font-medium ${tone.hint}`}>{copy.hint}</p>
          )}
        </div>
      </div>
      {onRetry && (
        <button
          onClick={onRetry}
          className="w-fit px-5 py-2.5 bg-gradient-to-tr from-orange-600 to-amber-500 hover:from-orange-700 hover:to-amber-600 rounded-xl text-xs font-bold tracking-wider text-white shadow-lg shadow-orange-600/20 flex items-center gap-2 transition-all active:scale-98 cursor-pointer focus:outline-none"
        >
          <RefreshCw className="w-3.5 h-3.5" />
          {copy.retryLabel ?? "Retry"}
        </button>
      )}
    </div>
  );
}
