package laravel

import (
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

const shopRoutes = `<?php
use Illuminate\Support\Facades\Route;
use App\Http\Controllers\TaxController;

$shopRoutes = static function () {
    Route::post('shop/ping', 'PingController')->name('ping'); // invokable
    // Route::get('commented/out', 'NopeController@index');
    /* Route::get('also/out', 'NopeController@index'); */
    Route::prefix('cart/{cart}')
        ->name('cart')
        ->middleware(['auth', Foo::class])
        ->group(function () {
            Route::prefix('items')->group(static function () {
                Route::get('/', 'ItemsController@index');
                Route::delete('/{item}', [ItemsController::class, 'destroy']);
            });
            Route::resource('notes', 'NotesController')->only('index', 'store');
            Route::resource('/', 'BranchController')->only(['index']);
            Route::match(['get', 'post'], 'tax', TaxController::class);
            Route::get('/', function () { return 'closure'; });
        });
};

Route::domain('pay.example')->prefix('gateway')->group($shopRoutes);
Route::shopRoute($shopRoutes);
Route::group(['prefix' => 'admin', 'middleware' => 'auth'], function () {
    Route::put('settings', 'SettingsController@update');
});
$unused = function () { Route::get('never', 'NeverController@index'); };
`

// @verifies entrypoints:INV-4
func TestScanRoutesFollowsGroupsAndMounts(t *testing.T) {
	var got []string
	lines := map[string]int{}
	for _, r := range ScanRoutes(shopRoutes, "api") {
		got = append(got, r.Method+" "+r.Path+" "+r.Handler)
		lines[r.Method+" "+r.Path] = r.Line
	}
	sort.Strings(got)
	var want []string
	for _, base := range []string{"/api/gateway", "/api"} {
		for _, r := range []string{
			"POST /shop/ping PingController::__invoke",
			"GET /cart/{cart}/items ItemsController::index",
			"DELETE /cart/{cart}/items/{item} ItemsController::destroy",
			"GET /cart/{cart}/notes NotesController::index",
			"POST /cart/{cart}/notes NotesController::store",
			"GET /cart/{cart} BranchController::index",
			"GET /cart/{cart}/tax App\\Http\\Controllers\\TaxController::__invoke",
			"POST /cart/{cart}/tax App\\Http\\Controllers\\TaxController::__invoke",
			"GET /cart/{cart} ",
		} {
			m, rest, _ := cut(r)
			want = append(want, m+" "+base+rest[1:])
		}
	}
	want = append(want, "PUT /api/admin/settings SettingsController::update")
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got:\n%v\nwant:\n%v", got, want)
	}
	if lines["POST /api/shop/ping"] != 6 || lines["PUT /api/admin/settings"] != 27 {
		t.Errorf("lines = %v", lines)
	}
}

func cut(r string) (string, string, bool) {
	for i := range r {
		if r[i] == ' ' {
			return r[:i], " " + r[i+1:], true
		}
	}
	return r, "", false
}

func TestScanRoutesReviewCases(t *testing.T) {
	keys := func(src string) []string {
		var out []string
		for _, r := range ScanRoutes(src, "") {
			out = append(out, r.Method+" "+r.Path+" "+r.Handler)
		}
		sort.Strings(out)
		return out
	}
	cases := map[string]struct {
		src  string
		want []string
	}{
		"arrow fn and where args are not routes": {`<?php
Route::get('/a', fn ($r) => $r->get('x'));
Route::get('/ok', [A::class, 'b'])->where('id', $req->get('z'));`, []string{"GET /a ", "GET /ok A::b"}},
		"fluent prefix before the verb": {`<?php
Route::middleware('auth')->prefix('m')->get('/x', 'X@y');`, []string{"GET /m/x X::y"}},
		"comments inside arguments": {`<?php
Route::get('/a', // was [Old::class, 'x']
  [New::class, 'y']);
Route::get(/* v1 */ '/b', 'B@c');`, []string{"GET /a New::y", "GET /b B::c"}},
		"resource options array": {`<?php
Route::resource('photos', P::class, ['only' => ['index']]);`, []string{"GET /photos P::index"}},
		"self-mounting closure stops": {`<?php
$f = function () use (&$f) { Route::get('x', 'X@y'); Route::prefix('a')->group($f); Route::prefix('b')->group($f); };
Route::group([], $f);`, nil},
		"reassigned variable": {`<?php
$r = function () { Route::get('x', 'X@x'); };
Route::prefix('a')->group($r);
$r = function () { Route::get('y', 'Y@y'); };
Route::prefix('b')->group($r);`, []string{"GET /a/x X::x", "GET /b/y Y::y"}},
		"attribute is not a comment": {`<?php
#[Deprecated] Route::get('/attr', 'A@b');`, []string{"GET /attr A::b"}},
		"macro body is not registered": {`<?php
Route::macro('shop', function () { Route::get('inside', 'I@i'); });`, nil},
	}
	for name, c := range cases {
		got := keys(c.src)
		if name == "self-mounting closure stops" {
			if len(got) > 50 {
				t.Errorf("%s: %d routes", name, len(got))
			}
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %q want %q", name, got, c.want)
		}
	}
	// an unclosed call per line must not scan to the end of the file each time
	big := "<?php\n" + strings.Repeat("Route::get('/x', [A::class, 'b';\n", 20000)
	start := time.Now()
	ScanRoutes(big, "")
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("unclosed calls took %s", d)
	}
}
