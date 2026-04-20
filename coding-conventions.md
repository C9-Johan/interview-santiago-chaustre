# Coding conventions

## Control flow: guard clauses, no `else`

Treat `else` as banned by default. Every branch that terminates (returns, continues, breaks, panics, `t.Fatal`) does not take an `else`:

```go
// Bad
if err != nil {
    return err
} else {
    doWork(x)
}

// Good
if err != nil {
    return err
}
doWork(x)
```

Chain validation as flat guard clauses — the happy path runs at the minimum indentation of the function:

```go
func (p *processor) Submit(ctx context.Context, in Input) (Output, error) {
    if in.TenantID == "" {
        return Output{}, ErrMissingTenant
    }
    if !in.Amount.IsPositive() {
        return Output{}, ErrNonPositiveAmount
    }
    tenant, err := p.tenants.Get(ctx, in.TenantID)
    if err != nil {
        return Output{}, fmt.Errorf("get tenant: %w", err)
    }
    // happy path …
}
```

`switch` is acceptable (and preferred) over `else if` ladders. `switch` without a tag reads like chained guards.

## Interfaces

> *“Accept interfaces, return structs. Interfaces should live close to where they're consumed, not where they're implemented.”*

**Default to unexported interfaces, declared in the consumer package.** A processor that needs to load tenants declares its own tiny interface:

```go
// application/invoicing/processor/submit.go
package processor

type tenantLoader interface {
    Get(ctx context.Context, id string) (*model.Tenant, error)
}

type Submitter struct {
    tenants tenantLoader
}
```

Mongo and Valkey implementations satisfy that interface structurally — no imports back into the processor package.

**Export an interface only when:**

- Multiple implementations are selected at runtime or in tests (data stores, DIAN client, object store, signer, notification channel).
- The interface is the public contract that allows alternative backends to be plugged in.

Exported contracts live in `pkg/domain/repository/` (repositories, DIAN client, object store, signer, XML builder). Do **not** add an exported interface merely to allow mocking — declare an unexported interface in the consumer and let `mockery` generate a mock from that.

### What goes into exported interfaces

- Small (ideally ≤5 methods). Split when they grow.
- Behavior-oriented names (`InvoiceRepository`, not `InvoiceDAO`).
- Context-first (`ctx context.Context` as the first parameter on every method).
- Domain types in / domain types out — never transport or persistence types.

## Type safety: prefer generics, avoid `any` and `reflect`

> *"Make the compiler your first test. Every `any` moves a potential error from compile time to runtime."*

Go 1.26 generics cover most of the cases where older Go code reached for `interface{}`. Use them. Reflection is a last resort, not a convenience.

### Avoid `any` / `interface{}` in production code

Red flags that should trigger a refactor:

- A function that returns `any`.
- A pair of functions whose return values are meant to be consumed together (e.g. a "workflow func" and its "input" returned from sibling switches) — the compiler can't enforce the pairing.
- `map[string]any` payloads shuttled across package boundaries.
- `interface{}` parameters followed by a type switch inside the function.

**Use a type parameter instead.** When a caller dispatches to one of N typed pairs, express the pair as a generic struct:

```go
// Pair a workflow function with its matching input constructor. The
// shared type parameter I correlates both sides at compile time: a
// signature drift on either side is a compile error at the call site.
type typedDispatcher[I any] struct {
    wf      func(workflow.Context, I) error
    mkInput func(Source) I
}

func (d typedDispatcher[I]) Start(ctx workflow.Context, s Source) workflow.ChildWorkflowFuture {
    return workflow.ExecuteChildWorkflow(ctx, d.wf, d.mkInput(s))
}

// Heterogeneous registry via an interface the generic type implements.
type dispatcher interface {
    Start(workflow.Context, Source) workflow.ChildWorkflowFuture
}

var registry = map[string]dispatcher{
    "invoice":     typedDispatcher[InvoiceInput]{wf: ProcessInvoice, mkInput: invoiceInput},
    "credit_note": typedDispatcher[CreditNoteInput]{wf: ProcessCreditNote, mkInput: creditNoteInput},
    // ...
}
```

The canonical live example is `typedOrphanDispatcher[I]` in `temporal/workflows/reconciliation.go`.

### Narrow, documented exceptions where `any` is OK

1. **The boundary of an externally-defined untyped API.** Temporal's `workflow.ExecuteChildWorkflow(ctx, wf any, args ...any)` takes `any` — we pass typed values in, but the SDK's signature is set outside our control.
2. **A JSON envelope whose schema varies by caller** (e.g. an audit-log payload or a generic notification body). Keep the `any` at the serialization boundary and unmarshal into a typed struct as early as possible on the consumer side.

The bar for an exception: *a reviewer can read one line of comment and see why a type parameter doesn't fit here.* If you can't write that sentence, refactor.

### Avoid `reflect` in production code

Reflection is allowed for generic utility functions that genuinely cannot be expressed with type parameters (deep-equal helpers, the odd test-only assertion). Everything else should reach for generics, interfaces, or code generation first.

Why we're strict:

- Reflection errors manifest at runtime, not compile time — they bypass `go vet`, `golangci-lint`, and most test coverage.
- Reflection disables most static analysis and IDE refactoring. A rename no longer finds reflection-based callers.
- Reflection is slow, and slow in places that are easy to miss on a profile.

If you must add a `reflect` import in `pkg/`, `application/`, `temporal/`, or `cmd/`, include a comment explaining why a type parameter or interface won't do. Reviewers will treat it as a design concern, not a detail.

## Mappers at layer boundaries

Do not share structs across layers. Map.

- `pkg/domain/mappers/fromgrpc/` — request protobuf → domain model
- `pkg/domain/mappers/frommodel/` — domain model → response protobuf
- `repository/mongo/<entity>/mapper.go` — domain ↔ BSON persistence doc

Mappers are pure functions, covered by table-driven tests, and never reach network, database, or filesystem.

## GoDoc

Every exported identifier carries a GoDoc comment. First sentence starts with the identifier name and describes behavior, not implementation:

```go
// InvoiceRepository persists and retrieves invoices for a tenant.
// Implementations must be safe for concurrent use.
type InvoiceRepository interface { … }

// Submit validates the invoice, signs it with the tenant's certificate,
// and enqueues it for DIAN transmission. It returns ErrTenantNotFound
// if the tenant does not exist.
func (s *Submitter) Submit(ctx context.Context, in Input) (Output, error) { … }
```

- Document invariants, concurrency expectations, and which errors are returned (reference sentinels directly).
- Document any side effect that is not obvious from the signature (writes, external calls, retries).
- Do not document parameters that are obvious from their names. Do not restate what the code already says.

Unexported identifiers may carry a one-line comment if the *why* is non-obvious; otherwise leave them bare.

## Naming and package layout

- Package names are lowercase, short, and describe *what is in them*, not what they do (`invoice`, not `invoicemanager`).
- One concept per file when files grow past ~300 lines.
- Avoid stutter: `invoice.New`, not `invoice.NewInvoice`.
- Constructors return concrete types, never interfaces (“accept interfaces, return structs”).

## Concurrency

- `context.Context` is the first parameter on any function that does I/O or can block.
- Never store a `context.Context` in a struct. Pass it through.
- Every goroutine has a clear owner and a termination condition tied to a context or a channel close.
- Guard shared mutable state with `sync.Mutex` or a single-owner channel pattern; race detector must stay green (`go test -race`).

## Formatting and linters

`make fix-format` runs `goimports` (local prefix `github.com/wogosoft`), `gofumpt`, and `golines` (120 cols). CI enforces `golangci-lint`, `go vet`, and `gosec`.

Do not suppress findings. `//nolint` and `// #nosec` are forbidden in non-generated code; fix the underlying issue or adjust the design. The only accepted exclusions are the path-based exclusions already configured in `Makefile` and `lefthook.yml` (`mocks/`, `gen/`, `vendor/`, `cmd/setup/`).

For common lint categories:

- `errcheck` — never ignore an error. If you truly intend to discard, assign to `_` with a one-line comment explaining why.
- `gosec` G104 (unhandled errors), G304 (path inclusion), G401/G501 (weak crypto) — address the root cause; crypto is constrained by DIAN (see `pkg/signing/`), not by convenience.
- `revive` / `staticcheck` — follow suggestions. If a suggestion conflicts with a DIAN-mandated shape, document the reason in-code in one line.

## Writing code that passes the quality gate

`golangci-lint` enforces a Sonar-style gate on every commit, push, and PR. These rules exist because the patterns LLM-generated Go regularly gets wrong (mega-functions, copy-paste blocks, unused params, deep nesting) silently rot a codebase. Write to the gate from the start; do not write a 200-line function and then try to "split it later".

**Functions stay small.**

- Aim for under 50 statements / 100 lines. If a function is approaching the limit, it is doing two things — extract one of them.
- One responsibility per function. If you find yourself writing a comment like `// now do X` halfway through, that is the seam: extract the X part.
- A function with five sequential phases (parse → validate → transform → call → assemble) is five functions. The top-level function becomes a table of contents.

**Keep complexity flat.**

- Cyclomatic complexity > 30 means too many branches in one body. Reach for early returns, lookup maps, polymorphism, or a smaller helper.
- Cognitive complexity > 20 means the reader has to hold too much state. Nesting, negated conditions, and mixed concerns inflate cognitive faster than cyclomatic — flatten them.
- Maximum nesting depth is 5 (`nestif`). If you are at 4, refactor before you reach 5; the next person to touch this code will not have your context.
- Use guard clauses (see "Control flow" above). Every `if err != nil { return err }` at the top of a function buys you one less indent level for the happy path.

**Do not copy-paste.**

- `dupl` flags ~150-token clones. If you are about to copy a block from one file to another, stop and extract a helper instead. This is the single most common failure mode in LLM-written Go.
- Generics (`func mapLines[T any](...)`) are the right tool when two functions differ only in their output type. See `pkg/domain/mappers/fromgrpc/note_common.go` for the canonical example.
- For exception cases — two files that *must* stay structurally similar because they map to distinct spec sections (CUFE vs CUDE per Anexo Técnico v1.9) — add a path-level exclusion in `.golangci.yml` with a comment explaining why. Do not paper over with `//nolint` (forbidden).

**Do not pad with dead weight.**

- Unused parameters (`revive: unused-parameter`): rename to `_`. If the parameter is mandated by an interface but truly unused in this implementation, that signals the interface is wider than it needs to be — but in the meantime, `_` is the right rename.
- Magic strings/numbers reused 3+ times become a `const`. `goconst` will flag this.
- `else` after a terminating branch is banned (see "Control flow" above and AGENTS.md rule 2).

**Performance hygiene from the linters.**

- Pass structs > 256 bytes by pointer (`gocritic: hugeParam`). Below that, value semantics are usually fine and avoid GC pressure.
- Range loops over slices of large structs use index form: `for i := range xs { x := &xs[i] }` (`gocritic: rangeValCopy`).
- Wrap errors with `%w`, not `%v` (`errorlint`). Compare wrapped errors with `errors.Is` / `errors.As`, never `==`.
- Close `http.Response.Body` (`bodyclose`).
- Pre-allocate slices when you know the size (`prealloc`).

**Test files are exempt from size/dup checks.** Table-driven tests legitimately get long and repetitive; that is the design pattern, not a code smell. Correctness linters still apply.

**When the gate is wrong.** If a threshold genuinely does not fit a piece of code, raise it deliberately in `.golangci.yml` with a code comment explaining the rationale. Do not add `//nolint` comments and do not split the work to bypass the gate. Talk to a teammate first if the change feels load-bearing.

## Code-quality thresholds (Sonar-equivalent)

The active limits enforced by `.golangci.yml` (source of truth):

| Metric | Linter | Limit |
|---|---|---|
| Cyclomatic complexity per function | `cyclop` | 30 |
| Cyclomatic complexity (package average) | `cyclop` | 10 |
| Cognitive complexity per function | `gocognit` | 20 |
| Function length (lines) | `funlen` | 100 |
| Function length (statements) | `funlen` | 50 |
| Code duplication threshold (tokens) | `dupl` | 150 |
| Maximum nested-`if` depth | `nestif` | 5 |
| Maintainability index minimum | `maintidx` | 20 |
| Magic-string / number reuse threshold | `goconst` | min-len 3, min-occurrences 3 |
| `hugeParam` / `rangeValCopy` byte threshold | `gocritic` | 256 |

Test files (`_test.go`) are exempt from the size, complexity, and duplication checks because the table-driven test pattern legitimately produces long, repetitive, complex bodies. Correctness-focused linters (`errcheck`, `staticcheck`, `govet`, `revive`, `errorlint`, …) still apply to tests.

If a threshold is genuinely wrong for a piece of code, raise it deliberately in `.golangci.yml` with a code comment explaining the rationale. Do **not** add `//nolint` directives or paper over individual violations.
