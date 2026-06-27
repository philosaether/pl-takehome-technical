### Lead

This year, the Honcho application saw an orders-of-magnitude explosion in use, from ~200 active users to ~30,000. While its existing proof-of-concept architecture has held up admirably, operational maturity will require a deliberate choice of implementation methodology.

In this paper, we propose a two-stage queue migration plan for the Honcho application. The first stage represents a minimal, [N]-line alteration to the existing Postgres queue code, which will result in a ~1000x increase in performance sufficient to support another orders-of-magnitude growth cycle.

The second stage would move some or all of the work processed by Honcho to a Valkey-backed in-memory queue. While it increases operational complexity and increases the risk of data loss, the Valkey implementation has been experimentally shown to offer up to 15x performance over Postgres under certain conditions.

### §1 - Low-hanging fruit is the most nutritious
- Results and guidance from lookahead test; figs 1 + 2

### §2 - Efficiency is (sometimes) worth the effort
- Results and guidance from run-cloud-2; fig 3

### §3 - Methods
- Experimental setup + cloud spend estimate