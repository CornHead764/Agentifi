# Categorization evaluation

Stop the server first: evaluation takes the data-directory instance lock. Use
an existing household UUID from Settings and the same configuration as the
server (including the credential encryption key if opting into the model).

```sh
DATA_DIR=/path/to/data agentifi eval categorize --household <uuid> --out categorize.json
DATA_DIR=/path/to/data agentifi eval categorize --household <uuid> --holdout 0.3
DATA_DIR=/path/to/data agentifi eval categorize --household <uuid> --cutoff 2026-01-01
# Sends held-out rows and training context to the household's configured model:
DATA_DIR=/path/to/data agentifi eval categorize --household <uuid> --with-model --out categorize-model.json
```

Without `--with-model` the command makes no network calls and needs no model
connection. Rules run first, followed by the same history assessment and
category decision used by Suggest categories. The optional model uses the
built-in Suggest categories prompt, tools and confidence threshold (0.85),
not a household's edited automation. Model failures stop the evaluation;
they are not counted as abstentions. Raw model errors are omitted for privacy.

The command snapshots server metadata and the selected household into a
private temporary directory and deletes that directory when it returns. It
does not edit the source ledger. Allow space for those two SQLite files.
The snapshot contains private financial data; protect temporary storage as
you protect DATA_DIR. An interrupted or killed process may leave its private
`agentifi-eval-*` temporary directory behind.

Labelled postings have a category that can be suggested and a reviewed flag
that `ReviewedCategoryStands` accepts. Reviewed imports count; accounts whose
rows are born reviewed do not. Transfers and pending, deleted or projected
rows do not supply labels. A split parent supplies its labelled splits,
each with its own amount and the parent's statement wording.

The default holds out the newest 20% of labelled postings by effective date,
rounded up. A whole boundary day is held out, so the actual fraction may be
larger. `--cutoff` overrides `--holdout`. Empty training or test sets are an
error. History includes all eligible ledger rows strictly before the cutoff,
not only labelled rows: uncategorized and transfer evidence matter to the
live vote. Held-out payees, notes, tags, categories, rule and series links,
and previous checks are hidden. Each held-out posting is presented alone;
its prediction never becomes training evidence for a later posting. Relative
date presets resolve on that posting's effective date rather than today.

The report contains counts, coverage (suggested / held out), accuracy
(correct / suggested), per-basis and confidence-band metrics, and up to 20
wrong-category confusions. Group coverage is its suggestions / all held-out
postings, not its suggestions / its own group. Ratios are fractions; an empty
denominator gives JSON `null` and a summary of `n/a`, not zero accuracy. Rules have unknown
confidence. Category names are the only ledger text in the report and human
summary; statement names, payees and amounts are never included. JSON goes
to stdout or `--out`; the human summary goes to stderr. Report files are
replaced atomically with private permissions and must be outside DATA_DIR.

This is a frozen-history comparison, not a historical database reconstruction.
Category vocabulary and account metadata use their current versions, and
pre-cutoff transaction labels may have been edited later: review timestamps
are not retained. Rules, guidance and their filters changed since the cutoff
are omitted because their older revisions cannot be recovered. Corrections
and merchant attachments are omitted. Split proposals count as abstentions
for a single posting. These limitations are repeated in the JSON report.
Compare reports with the same dataset, cutoff, model settings and binary
commit. `pipeline_version` identifies the evaluation protocol; `commit`
identifies stamped builds. Some development builds do not carry a VCS
stamp. If `commit` is empty,
record the checkout commit and uncommitted changes alongside the report, or
supply the existing build stamp explicitly:

```sh
cd backend
nix develop .. -c go build -ldflags "-X github.com/CornHead764/agentifi/backend/internal/buildinfo.Commit=$(git rev-parse HEAD)" -o agentifi ./cmd/agentifi
```

A linker stamp does not prove the tree was clean; record any uncommitted
changes separately.

Invented seeded data exercises the command but is not a quality benchmark:
its category assignments and review patterns do not model a person's habits.
