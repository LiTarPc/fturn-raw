# Pion TURN maintenance patch

Base: github.com/pion/turn/v5 v5.0.12, copied from the Go module archive pinned in the parent go.sum. Upstream MIT license retained. No edits to the global Go module cache.

Changes: expose optional BindingRefreshInterval, BindingCheckInterval and AllocationRefreshInterval; cap requested allocation refresh at the upstream half-lifetime; report non-438 allocation Refresh errors instead of returning nil; log ChannelBind requests. Zero options keep legacy timer defaults. The parent Raw client alone enables earlier staggered maintenance and selected diagnostics.
