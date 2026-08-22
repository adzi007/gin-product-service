# Task: Move HTTP DTOs out of `internal/domain` (Phase 2)

Source: `issue.md`, "Phase 2 — Move HTTP DTOs out of `domain`".

## Goal

Right now, structs in `internal/domain/product.go` and `internal/domain/variant.go`
carry Gin/validator-specific tags (`binding:"..."`, `validate:"..."`) alongside
`json:"..."`. These are HTTP transport concerns, not domain concerns. `internal/domain`
should only contain entities and interfaces — see `CLAUDE.md`'s Architecture section.

By the end of this task:
- No struct in `internal/domain/*.go` has a `binding:` or `validate:` tag.
- All HTTP request-body/query DTOs live in a new package, e.g.
  `internal/delivery/http/dto`.
- Handlers in `internal/delivery/http/handler/product_handler.go` bind the incoming
  JSON into a `dto.*` struct, validate it there, then convert it into the existing
  plain `domain.*` input struct before calling the use case.
- Use case, repository, and test signatures in `internal/app/product/*` and
  `internal/infrastructure/repository/productRepo.go` are **unchanged** — they keep
  accepting the same `domain.*` structs as today, just with `binding`/`validate` tags
  stripped from those structs.

This task does NOT include: splitting `ProductRepository` (Phase 6), adding aggregate
methods (Phase 4), introducing value objects (Phase 7), or moving read-model structs
like `ProductCategory`/`PaginatedProducts` (Phase 5). Do not touch those in this PR.

## Structs to move

These structs currently live in `internal/domain/product.go` and
`internal/domain/variant.go` and have `binding`/`validate` tags. Move all of them:

From `internal/domain/product.go`:
- `CreateProductInput`
- `ProductOptionInput`
- `GalleryMediaInput`
- `UpdateProductInput`
- `CreateOptionInput`
- `UpdateOptionInput`
- `ReorderOptionsInput`
- `PositionUpdate`
- `CreateOptionValueInput`
- `UpdateOptionValueInput`
- `CreateMediaInput`
- `BulkCreateMediaInput`
- `UpdateMediaInput`
- `AttachVariantMediaInput`
- `ReorderMediaInput`

From `internal/domain/variant.go`:
- `VariantInput`
- `VariantMediaInput`
- `CreateVariantInput`
- `VariantMediaItemInput`
- `BulkCreateVariantsInput`
- `UpdateVariantInput`
- `VariantUpdateItem`
- `BulkUpdateVariantsInput`
- `BulkDeleteVariantsInput`
- `ReorderVariantsInput`

Leave everything else in `product.go`/`variant.go` untouched (entities, repository
interfaces, use case interfaces, `ProductStatus`, `ProductOption`, etc.) — those don't
have `binding`/`validate` tags and are not in scope.

## Step-by-step

### 1. Create the new package

Create `internal/delivery/http/dto/` with two files: `product_dto.go` and
`variant_dto.go` (mirroring the domain file split).

### 2. Copy each struct into the DTO package, keep tags as-is

For every struct listed above, copy its full definition (fields, `json` tags,
`binding` tags, `validate` tags — copy them verbatim) into the matching `dto` file.
Rename nothing yet. Example — `CreateOptionInput` moves from
`internal/domain/product.go` to `internal/delivery/http/dto/product_dto.go` unchanged:

```go
// internal/delivery/http/dto/product_dto.go
package dto

type CreateOptionInput struct {
	Name   string   `json:"name" binding:"required" validate:"required"`
	Values []string `json:"values" binding:"required" validate:"required,min=1"`
}
```

Some of these structs reference other structs in the list (e.g.
`CreateProductInput.Options []ProductOptionInput`, `CreateProductInput.Variants
[]VariantInput`). Since all referenced structs are also moving into `dto`, these
internal references stay valid — just make sure the referenced struct also exists in
`dto` before you compile.

Watch for cross-file references between the two lists — e.g. `CreateProductInput` (in
`product_dto.go`) references `VariantInput` (which moves to `variant_dto.go`). Since
both files are `package dto`, this works with no import needed, same as it does today
within `package domain`.

### 3. Delete the original structs from `internal/domain`

Once copied, delete each struct definition from `internal/domain/product.go` and
`internal/domain/variant.go`. Do not delete anything else from those files.

After deletion, `go build ./...` will fail everywhere these types were referenced —
that's expected and is what step 4 and 5 fix.

### 4. Add a plain (tag-free) input struct in `domain` for each DTO, if one doesn't already exist

Check first: some use cases may already accept a different, plain struct (e.g.
`domain.CreateProductParams`). If a plain equivalent already exists, reuse it — don't
create a duplicate.

Where no plain equivalent exists yet, add one to the domain file with the same fields
but no `binding`/`validate` tags, and no Gin/validator dependency. Keep a `json` tag
only if the use case or repository layer relies on it (e.g. for logging/serialization);
otherwise drop tags entirely — domain structs shouldn't need `json` either, but don't
widen this task's scope by ripping out `json` tags that other code depends on. If
unsure, keep the plain struct's field names/types identical to the DTO and leave `json`
tags in place; only `binding`/`validate` are required to disappear from `domain`.

Concretely, for each struct in the list in "Structs to move", after moving it to `dto`,
check every call site (`grep -rn "domain\.<StructName>" internal/`) to see what already
consumes it:
- If a use case function signature takes `domain.<StructName>` directly, you must keep
  a same-named plain struct in `domain` for it to keep compiling — copy the struct back
  into `domain` minus the `binding`/`validate` tags.
- If nothing outside the handler ever consumed `domain.<StructName>` beyond the handler
  binding it from the request body, no plain domain struct is needed — the handler can
  map DTO fields directly into whatever the use case already expects (e.g.
  `domain.CreateProductParams`).

In practice, for this codebase, most of these structs ARE what the use cases take as
parameters directly (confirmed via grep — `internal/app/product/insertUseCase.go`,
`updateUseCase.go`, `optionUseCase.go`, `variantUseCase.go`, `mediaUseCase.go`, and
`internal/infrastructure/repository/productRepo.go` all reference these types by name).
So for nearly every struct in the list, you'll end up with two versions:
- `dto.CreateOptionInput` — has `binding`/`validate`, used only in the handler.
- `domain.CreateOptionInput` — same fields, no `binding`/`validate`, used by the use
  case/repository (this is just today's struct with two tag types removed).

### 5. Update the handler to bind → validate → convert → call use case

In `internal/delivery/http/handler/product_handler.go`, for every handler method that
currently does something like:

```go
var input domain.CreateOptionInput
if err := c.ShouldBindJSON(&input); err != nil { ... }
// ... calls useCase.Something(ctx, input)
```

change it to:

```go
var req dto.CreateOptionInput
if err := c.ShouldBindJSON(&req); err != nil { ... }
input := domain.CreateOptionInput{
	Name:   req.Name,
	Values: req.Values,
}
// ... calls useCase.Something(ctx, input)
```

Write a small `toDomain()`-style conversion per DTO struct (either as a method on the
DTO, e.g. `func (r dto.CreateOptionInput) ToDomain() domain.CreateOptionInput`, or as a
free function in the handler file — pick whichever the reviewer prefers, but be
consistent across all conversions in this PR). Do this for every handler method touching
a struct in the "Structs to move" list.

Add the new import: `"github.com/<module-path>/internal/delivery/http/dto"` (check
`go.mod` for the exact module path) alongside the existing `domain` import in
`product_handler.go`.

### 6. Fix remaining compile errors

Run:

```bash
go build ./...
```

Fix any remaining reference to a moved struct. Expect to touch:
- `internal/delivery/http/handler/product_handler_test.go` (tests likely construct
  `domain.CreateProductInput{...}` directly to build request bodies — these should now
  construct `dto.CreateProductInput{...}`, `json.Marshal` it, and send it as the request
  body, since that's what a real client sends).
- Any other test file under `internal/app/product/*_test.go` — these should generally
  keep using `domain.*` structs since use cases still take `domain.*` inputs. Only
  change these if `go build`/`go vet` reports an actual error.

### 7. Verify

```bash
go build ./...
go vet ./...
go test ./...
```

All must pass. Also grep to confirm no domain struct still has a validator tag:

```bash
grep -rn "binding:\|validate:" internal/domain/
```

This must return no results.

### 8. Manual sanity check

Start the server (`air` or `go run cmd/main.go`) and hit one product-creation and one
variant-update endpoint with `curl`/Postman using both a valid and an invalid payload
(e.g. missing a `required` field) to confirm validation errors still surface the same
way as before the refactor (same HTTP status code and error shape).

## Notes for whoever picks this up

- This is a mechanical, low-risk refactor per the source proposal — no behavior should
  change, only where types live. If you find yourself wanting to change use case logic,
  stop — that's out of scope for this task.
- Don't rename any struct or field while moving it. Keep this PR reviewable as "move +
  strip tags", not "move + redesign".
- If a struct in the list turns out to be unused anywhere (dead code), flag it in the PR
  description instead of silently deleting it.
