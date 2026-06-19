/**
 * @license
 * SPDX-License-Identifier: Apache-2.0
 */

import React from "react";

export default function GlowBackground() {
  return (
    <div className="fixed inset-0 -z-50 overflow-hidden bg-[#050505]">
      {/* Decorative ambient radial grids matching the Sophisticated Dark prompt */}
      <div 
        className="absolute inset-0 opacity-20 pointer-events-none"
        style={{
          backgroundImage: `
            radial-gradient(circle at 10% 20%, rgba(249, 115, 22, 0.08) 0%, transparent 40%),
            radial-gradient(circle at 90% 80%, rgba(99, 102, 241, 0.08) 0%, transparent 40%),
            radial-gradient(circle at 50% 50%, rgba(245, 158, 11, 0.04) 0%, transparent 60%)
          `
        }}
      />

      {/* Floating high-contrast blurred blobs representing key Sophisticated Dark atmosphere */}
      <div className="absolute top-[-10%] left-[-10%] w-[50%] h-[50%] bg-orange-600/10 rounded-full blur-[120px] pointer-events-none animate-drift-slow" />
      <div className="absolute bottom-[-10%] right-[-10%] w-[50%] h-[50%] bg-indigo-600/10 rounded-full blur-[120px] pointer-events-none animate-drift-slower" />
      <div className="absolute top-[40%] left-[30%] w-[350px] h-[350px] rounded-full bg-amber-500/5 blur-[100px] pointer-events-none animate-drift-slow" />

      {/* Futuristic subtle scanning horizontal grid lines */}
      <div 
        className="absolute inset-0 pointer-events-none opacity-[0.02]"
        style={{
          backgroundImage: `linear-gradient(rgba(255, 255, 255, 0.1) 1px, transparent 1px), linear-gradient(90deg, rgba(255, 255, 255, 0.1) 1px, transparent 1px)`,
          backgroundSize: "64px 64px"
        }}
      />
    </div>
  );
}
