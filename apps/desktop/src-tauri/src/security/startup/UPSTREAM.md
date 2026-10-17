# Startup detector provenance

移植自 KnockKnock，Objective-See, LLC，GPL-3.0。
Upstream: https://github.com/objective-see/KnockKnock/tree/914f2c0c2d3f058f40b6d98ed303e8a1a6715eb3
Commit is locked in `upstream.json` with source file SHA-256 hashes.
Original license: `LICENSE.KnockKnock` (unmodified).

Original copyright statements retained:

- Copyright (c) 2015 Objective-See. All rights reserved. (Cronjobs, LogInOutHooks, DirectoryServicesPlugins, QuicklookPlugins)
- Copyright (c) 2018 Objective-See. All rights reserved. (EventRules)
- Copyright (c) 2023 Objective-See. All rights reserved. (BTM)

The 20 detector categories map to `StartupCategory` in the same order as the plan:
LaunchItems, BTM, LoginItems, Cronjobs, BrowserExtensions, Kexts,
SystemExtensions, Extensions, AuthorizationPlugins, DirectoryServicesPlugins,
SpotlightImporters, QuicklookPlugins, DockTiles, DylibInserts, DylibProxies,
LogInOutHooks, PeriodicScripts, StartupScripts, ShellConfigs, EventRules.

OpenNavo changes (2026-10-16): Rust port; bounded reads; no symlink/cloud
placeholder traversal; no VirusTotal or other cloud reputation requests; injectable
roots/commands; local review baseline; explicit missing categories; BTM v16/v18
archive traversal without object instantiation. Disabled launch items remain in
the list so users can enable them again. Quick Look statistics are obtained through
`qlmanage -m plugins`, rather than loading the private QuickLook framework.
The scanner operates as the current user and never requests root access to scan.
Mutation is separate and restricted to validated fixed actions.

No upstream product name or artwork is used in the product interface.
