/**
 * @license
 * SPDX-License-Identifier: Apache-2.0
 */

import React, { useState } from "react";
import { Review } from "../types";
import { Star, Send, MessageSquarePlus, User, AlertCircle, CircleCheck } from "lucide-react";
import { motion, AnimatePresence } from "motion/react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { MOCK_REVIEWS } from "../data/videos";

interface ReviewsSectionProps {
  videoId: string;
}

export default function ReviewsSection({ videoId }: ReviewsSectionProps) {
  const queryClient = useQueryClient();
  const [commentText, setCommentText] = useState("");
  const [selectedRating, setSelectedRating] = useState(5);
  const [showNotification, setShowNotification] = useState(false);

  // TanStack Query to fetch reviews
  const { data: reviews = [], isLoading } = useQuery<Review[]>({
    queryKey: ["reviews", videoId],
    queryFn: async () => {
      // Simulate API lag
      await new Promise((resolve) => setTimeout(resolve, 350));
      const localReviews = localStorage.getItem(`reviews-${videoId}`);
      if (localReviews) {
        return JSON.parse(localReviews);
      }
      // Load from static file only if nothing in localStorage
      const filteredMocks = MOCK_REVIEWS.filter(r => r.videoId === videoId);
      localStorage.setItem(`reviews-${videoId}`, JSON.stringify(filteredMocks));
      return filteredMocks;
    }
  });

  // Mutation to add review
  const addReviewMutation = useMutation({
    mutationFn: async (newReview: { userName: string; rating: number; comment: string }) => {
      await new Promise((resolve) => setTimeout(resolve, 450)); // Simulated network latency
      
      const created: Review = {
        id: `r-user-${Date.now()}`,
        videoId,
        userName: newReview.userName || "GuestVoyager",
        userAvatar: "https://images.unsplash.com/photo-1535713875002-d1d0cf377fde?w=150&auto=format&fit=crop&q=80",
        rating: newReview.rating,
        comment: newReview.comment,
        timestamp: "Just now"
      };

      const updated = [created, ...reviews];
      localStorage.setItem(`reviews-${videoId}`, JSON.stringify(updated));
      return updated;
    },
    onSuccess: (updatedReviews) => {
      // Direct cache updates for maximum smoothness
      queryClient.setQueryData(["reviews", videoId], updatedReviews);
      setCommentText("");
      setSelectedRating(5);
      setShowNotification(true);
      setTimeout(() => setShowNotification(false), 3000);
    }
  });

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!commentText.trim() || addReviewMutation.isPending) return;

    addReviewMutation.mutate({
      userName: "Active Voyager",
      rating: selectedRating,
      comment: commentText.trim()
    });
  };

  return (
    <div className="w-full glass-panel rounded-2xl p-6 mt-6 flex flex-col gap-6">
      <div className="flex items-center justify-between border-b border-white/5 pb-4">
        <div className="flex items-center gap-2">
          <MessageSquarePlus className="w-5 h-5 text-orange-400" />
          <h3 className="font-display font-bold text-lg text-slate-100 tracking-wide">Voyager Reviews</h3>
        </div>
        <span className="text-[10px] font-mono text-slate-400 uppercase tracking-widest bg-white/5 px-2.5 py-1 rounded-full">
          {reviews.length} total comments
        </span>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-12 gap-8">
        {/* LEFT COLUMN: Add a new Review (Form) */}
        <form onSubmit={handleSubmit} className="lg:col-span-5 flex flex-col gap-4">
          <h4 className="text-xs font-mono font-semibold text-orange-400 uppercase tracking-widest">Transmit Reaction</h4>
          
          {/* Star selector */}
          <div className="flex flex-col gap-1.5">
            <label className="text-[11px] font-mono text-slate-300">Stellar Score Rating</label>
            <div className="flex items-center gap-1 bg-slate-950/40 p-2.5 rounded-xl border border-white/5 w-fit">
              {[1, 2, 3, 4, 5].map((star) => (
                <button
                  key={star}
                  type="button"
                  onClick={() => setSelectedRating(star)}
                  className="p-1 text-slate-500 hover:scale-110 transition-transform duration-200 focus:outline-none"
                >
                  <Star 
                    className={`w-6 h-6 transition-colors ${
                      star <= selectedRating 
                        ? "text-amber-400 fill-current" 
                        : "text-slate-600"
                    }`} 
                  />
                </button>
              ))}
            </div>
          </div>

          {/* Comment text area */}
          <div className="flex flex-col gap-1.5">
            <label className="text-[11px] font-mono text-slate-300">Review Message</label>
            <textarea
              value={commentText}
              onChange={(e) => setCommentText(e.target.value)}
              placeholder="What are your thoughts on this transmission? Discuss CGI, narrative arcs, acoustics..."
              rows={3}
              required
              className="bg-slate-950/45 text-slate-200 placeholder-slate-500 text-xs p-3.5 rounded-xl border border-white/5 focus:border-orange-500/50 focus:ring-1 focus:ring-orange-500/20 backdrop-blur-sm transition-all focus:outline-none resize-none"
            />
          </div>

          <button
            type="submit"
            disabled={!commentText.trim() || addReviewMutation.isPending}
            className="px-5 py-2.5 bg-gradient-to-r from-orange-600 to-amber-600 hover:from-orange-700 hover:to-amber-700 disabled:from-slate-800 disabled:to-slate-800 disabled:text-slate-500 rounded-xl text-xs font-semibold tracking-wider text-white shadow-lg shadow-orange-600/20 active:scale-98 transition-all flex items-center justify-center gap-2 focus:outline-none"
          >
            {addReviewMutation.isPending ? (
              <>
                <div className="w-3 w-3 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                Transmitting...
              </>
            ) : (
              <>
                <Send className="w-3.5 h-3.5" />
                Broadcast Review
              </>
            )}
          </button>

          {/* Toast Reaction alert block */}
          <AnimatePresence>
            {showNotification && (
              <motion.div
                initial={{ opacity: 0, y: 10 }}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0, y: 10 }}
                className="p-3 bg-emerald-500/10 border border-emerald-500/20 rounded-xl flex items-center gap-2 text-emerald-400"
              >
                <CircleCheck className="w-4 h-4 text-emerald-400 flex-shrink-0" />
                <span className="text-[11px] font-medium font-sans">Review uploaded to decentral pipeline cache!</span>
              </motion.div>
            )}
          </AnimatePresence>
        </form>

        {/* RIGHT COLUMN: Reviews List Feed */}
        <div className="lg:col-span-7 flex flex-col gap-4">
          <h4 className="text-xs font-mono font-semibold text-orange-400 uppercase tracking-widest block">Broadcast Logs</h4>

          {isLoading ? (
            <div className="flex flex-col gap-3 py-6 justify-center items-center">
              <div className="w-6 h-6 border-2 border-orange-400/30 border-t-orange-500 rounded-full animate-spin" />
              <span className="text-[11px] font-mono text-orange-400 tracking-wider">Syncing comment nodes...</span>
            </div>
          ) : reviews.length === 0 ? (
            <div className="p-8 text-center bg-slate-950/20 border border-white/5 rounded-2xl flex flex-col items-center justify-center gap-2">
              <AlertCircle className="w-7 h-7 text-slate-500" />
              <h5 className="font-display text-sm font-semibold text-slate-400">Silent Channel</h5>
              <p className="text-[11px] text-slate-500 max-w-xs">No feedback reported yet. Share your experience to bootstrap this cinematic node.</p>
            </div>
          ) : (
            <div className="flex flex-col gap-3.5 max-h-[350px] overflow-y-auto pr-1">
              <AnimatePresence initial={false}>
                {reviews.map((rev) => (
                  <motion.div
                    key={rev.id}
                    initial={{ opacity: 0, x: 20 }}
                    animate={{ opacity: 1, x: 0 }}
                    exit={{ opacity: 0, x: -20 }}
                    className="p-4 rounded-xl bg-slate-950/30 border border-white/5 flex flex-col gap-2.5 hover:border-white/10 transition-colors"
                  >
                    <div className="flex items-center justify-between gap-4">
                      {/* User Info card */}
                      <div className="flex items-center gap-2.5">
                        <img 
                          src={rev.userAvatar} 
                          alt={rev.userName} 
                          className="w-7 h-7 rounded-lg object-cover border border-white/10"
                          referrerPolicy="no-referrer"
                        />
                        <div className="flex flex-col">
                          <span className="text-[11px] font-semibold text-slate-200">{rev.userName}</span>
                          <span className="text-[9px] font-mono text-slate-500">{rev.timestamp}</span>
                        </div>
                      </div>

                      {/* Score stars */}
                      <div className="flex items-center gap-0.5 bg-amber-400/5 px-2 py-0.5 rounded-full border border-amber-400/10">
                        <Star className="w-3 h-3 text-amber-400 fill-current" />
                        <span className="text-[10px] font-bold text-amber-300 font-mono">{rev.rating}</span>
                      </div>
                    </div>

                    <p className="text-[11px] text-slate-350 leading-relaxed font-normal bg-slate-950/15 p-2 rounded">
                      {rev.comment}
                    </p>
                  </motion.div>
                ))}
              </AnimatePresence>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
