package fastapi

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestScanRoutesCoversCanonicalShapes proves the FastAPI patterns we
// support — @app.method, @router.method, decorators with extra kwargs,
// async def, plain def, websocket — all parse and a non-decorator
// function definition is NOT picked up.
func TestScanRoutesCoversCanonicalShapes(t *testing.T) {
	src := `
from fastapi import FastAPI, APIRouter

app = FastAPI()
router = APIRouter()

@app.get("/health")
def health():
    return {"ok": True}

@app.post("/refunds", response_model=Refund)
async def create_refund(payload: RefundIn):
    ...

@router.put("/users/{id}",
            response_model=User,
            dependencies=[Depends(auth)])
async def update_user(id: int):
    ...

@router.delete("/users/{id}")
def delete_user(id: int):
    ...

@app.websocket("/ws")
async def ws_handler(ws):
    ...

# a regular function — must not appear in matches
def helper():
    pass
`
	got := scanRoutes(src)
	gotKeys := make([]string, 0, len(got))
	for _, r := range got {
		gotKeys = append(gotKeys, r.method+" "+r.path+" "+r.handler)
	}
	sort.Strings(gotKeys)
	want := []string{
		"DELETE /users/{id} delete_user",
		"GET /health health",
		"POST /refunds create_refund",
		"PUT /users/{id} update_user",
		"WEBSOCKET /ws ws_handler",
	}
	sort.Strings(want)
	if !reflect.DeepEqual(gotKeys, want) {
		t.Errorf("scanRoutes mismatch.\ngot:\n  %s\nwant:\n  %s",
			strings.Join(gotKeys, "\n  "),
			strings.Join(want, "\n  "))
	}
}

func TestHTTPEntryPointID(t *testing.T) {
	cases := map[string]string{
		"POST /api/v2/refunds":     "ep.http.post.api.v2.refunds",
		"GET /users/{id}":          "ep.http.get.users.id",
		"GET /":                    "ep.http.get",
		"DELETE /items/{item_id}":  "ep.http.delete.items.item_id",
	}
	for in, want := range cases {
		parts := strings.SplitN(in, " ", 2)
		if got := httpEntryPointID(parts[0], parts[1]); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}
