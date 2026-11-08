# Design Philosophy — Clipboard Translator

> **Do not interrupt the work you are doing just to translate.**  
> **翻訳のために、今している作業を中断させない。**

## Why this exists

Clipboard Translator is built for everyday writing and reading, especially short messages, chats, and emails. Translation should be a small action within the user's current workflow—not a separate destination, window, or conversation.

The core interaction is intentionally simple:

1. Select text in the application already in use.
2. Press a hotkey.
3. Paste the translation where it is needed.

The translation model is a means to this experience, not the product's identity.

## Product principles

### 1. Preserve the user's flow

Avoid requiring app switching, extra windows, or repeated copy/paste steps for the common path. Settings and advanced features must not intrude on the default interaction.

### 2. Keep the common path minimal

A user should be able to benefit from the default setup without learning a complex interface. Additional options should remain optional and discoverable rather than mandatory.

### 3. Treat models as replaceable components

Translation models and runtimes will improve. Keep model-specific behavior isolated where practical so that improvements can be adopted without redesigning the core interaction. Model swaps still require compatibility and quality checks.

### 4. Prefer local processing and user control

Translate on the user's machine. Make network access explicit and limited to user-initiated setup, downloads, or update checks. Preserve clear controls for clipboard replacement and automatic paste.

### 5. Reliability is part of usability

Protect the user's clipboard, focus, and existing work. Fail safely; handle cancellation, backend crashes, and installation or update failures without surprising the user.

### 6. Evaluate features by their effect on interruption

Before adding a feature, ask:

- Does this reduce friction in an actual translation workflow?
- Does it keep the default experience as simple as it is today?
- Does it introduce risk to focus, clipboard contents, privacy, or reliability?
- Could it be an optional setting instead of a new required step?

A feature that adds more complexity than value should be reconsidered—even if it is technically interesting.

## Scope and status

This document captures the product's core intent and **proposed design principles for discussion**. It is not a claim that every principle has been formally adopted or fully implemented. Existing documented behavior and platform limitations remain authoritative until changes are explicitly accepted and implemented.
