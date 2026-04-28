# Minimal test fixture

Some prose.

```go {file=internal/models/product.go}
package models

type Product struct {
    ID string
}
```

More prose between blocks.

```go {file=internal/models/product.go}
type Account struct {
    ID string
}
```

A block with no marker — illustrative, must not be extracted.

```go
package illustrative
```

A block in another language with a marker.

```sql {file=schema.sql}
CREATE TABLE products (id UUID PRIMARY KEY);
```

A snippet referencing the placeholder module path:

```go {file=internal/api/handler.go}
package api

import "github.com/yourorg/myapp/internal/models"

type _ models.Product
```
