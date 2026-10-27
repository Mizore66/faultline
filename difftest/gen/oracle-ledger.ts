// Live-oracle ops for the slice 4a ledger tests: node-oracle.ts spreads them
// into its handlers after its own entries, so each slice 4a task edits only
// its own op file.

// Handler is node-oracle.ts's: it takes the request's args and may return a
// Promise. (Not imported: node-oracle.ts starts reading stdin when loaded.)
type Handler = (args: any) => unknown;

export const ops: Record<string, Handler> = {};
