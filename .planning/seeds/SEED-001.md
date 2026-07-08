---
planted_during: "Phase 5 discussion (v1.1 Tech Debt Cleanup), 2026-07-08"
trigger_when: "A new milestone considers adding a document/audio-transcription engine class, or a vertical/niche product surface on top of OctoConv's conversion pipeline"
---

# SEED-001: Lesson-recording analysis for tutors and language schools

A vertical product built on top of OctoConv's existing async job pipeline: a tutor/language-school uploads a lesson recording, the service returns a transcript, a list of the student's top mistakes, and a ready-made spaced-repetition deck for review.

## Why This Matters

Technically this is the same transcription-with-summary capability already discussed as a general future direction for OctoConv (a new engine class alongside image/document/av), but framed around one concrete buyer persona (tutors/language schools) who saves roughly an hour of manual review work per lesson. A vertical framing like this makes the requirements and success criteria much easier to pin down than a generic "add audio transcription" engine — there's a specific user, workflow, and value metric (time saved per lesson) to design against.

## When to Surface

- When scoping a future milestone that considers a new engine class (beyond image/libvips)
- If audio/video transcription is proposed as a generic capability — this seed argues for scoping the first version around this specific buyer instead of a generic API
- If the team is looking for a concrete wedge/beachhead customer to validate a new engine class before generalizing it

## Context Note

Raised mid-discussion during Phase 5 (v1.1 Tech Debt Cleanup) planning, which was explicitly scoped to closing v1.0 audit tech debt with no new capabilities. Deferred here rather than expanding v1.1's scope.
