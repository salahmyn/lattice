package laravel

import (
	"reflect"
	"sort"
	"testing"
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
