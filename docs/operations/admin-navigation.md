# Administrator navigation persistence

Requirement IDs: NODE-001, CFG-001
Work item IDs: VER-003, VER-005
Milestone: M3 Release Candidate
Related issues: #121, #123 (broader acceptance remains open)

Administrator menu destinations now have allowlisted hash routes under the existing secure administrator pathname. Menu and configuration-tab navigation update browser history; initialization and history events restore the selected page. Nodes use #/server/manage, servers #/server/machine, and system configuration #/config/system. An empty root keeps the existing server default; unknown routes do not grant access. Authentication and server authorization remain unchanged.

Existing theme/client configuration unsaved-change prompts also apply to history navigation. This preserves page selection, not unsaved form values or transient node filters/dialogs.

Verified with App unit tests, the administrator navigation Playwright suite on desktop/mobile (reload, direct link, back/forward), ESLint and the frontend build. Deploy and roll back through the existing main development pipeline only.
