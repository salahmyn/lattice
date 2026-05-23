package laravel

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestScanRoutesCoversCanonicalShapes asserts that the four Laravel
// route-registration shapes the v0.3.0-α detector targets all parse:
//   - Route::<verb>('path', 'Controller@method')          string form
//   - Route::<verb>('path', [Controller::class, 'method'])  array form
//   - Route::resource('users', UserController::class)     7 methods
//   - Route::apiResource(...)                             5 methods
func TestScanRoutesCoversCanonicalShapes(t *testing.T) {
	src := `<?php
use App\Http\Controllers\RefundController;
use App\Http\Controllers\Api\V2\UserController as ApiUser;

Route::post('/api/v2/refunds', 'App\Http\Controllers\RefundController@store');
Route::get('/api/v2/refunds/{id}', [RefundController::class, 'show']);
Route::resource('users', ApiUser::class);
Route::apiResource('posts', \App\Http\Controllers\PostController::class);
Route::get('/closure', function () { return 'no handler'; });
`
	uses := parseUseStatements(src)
	got := scanRoutes(src, uses)

	type want struct {
		method, path, handler string
	}
	wants := []want{
		{"POST", "/api/v2/refunds", "App\\Http\\Controllers\\RefundController::store"},
		{"GET", "/api/v2/refunds/{id}", "App\\Http\\Controllers\\RefundController::show"},
		// Route::resource('users', ApiUser::class) -> 7 routes on UserController.
		{"GET", "/users", "App\\Http\\Controllers\\Api\\V2\\UserController::index"},
		{"GET", "/users/create", "App\\Http\\Controllers\\Api\\V2\\UserController::create"},
		{"POST", "/users", "App\\Http\\Controllers\\Api\\V2\\UserController::store"},
		{"GET", "/users/{user}", "App\\Http\\Controllers\\Api\\V2\\UserController::show"},
		{"GET", "/users/{user}/edit", "App\\Http\\Controllers\\Api\\V2\\UserController::edit"},
		{"PUT", "/users/{user}", "App\\Http\\Controllers\\Api\\V2\\UserController::update"},
		{"DELETE", "/users/{user}", "App\\Http\\Controllers\\Api\\V2\\UserController::destroy"},
		// Route::apiResource('posts', ...) -> 5 routes (no create/edit).
		{"GET", "/posts", "App\\Http\\Controllers\\PostController::index"},
		{"POST", "/posts", "App\\Http\\Controllers\\PostController::store"},
		{"GET", "/posts/{post}", "App\\Http\\Controllers\\PostController::show"},
		{"PUT", "/posts/{post}", "App\\Http\\Controllers\\PostController::update"},
		{"DELETE", "/posts/{post}", "App\\Http\\Controllers\\PostController::destroy"},
	}

	gotKey := func(r httpRoute) string {
		return r.method + " " + r.path + " " + r.handler.fqn
	}
	gotKeys := make([]string, 0, len(got))
	for _, r := range got {
		gotKeys = append(gotKeys, gotKey(r))
	}
	sort.Strings(gotKeys)

	wantKeys := make([]string, 0, len(wants))
	for _, w := range wants {
		wantKeys = append(wantKeys, w.method+" "+w.path+" "+w.handler)
	}
	sort.Strings(wantKeys)

	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Errorf("scanRoutes mismatch.\ngot:\n  %s\nwant:\n  %s",
			strings.Join(gotKeys, "\n  "),
			strings.Join(wantKeys, "\n  "))
	}
}

// TestParseUseStatements covers both unaliased and aliased imports.
func TestParseUseStatements(t *testing.T) {
	src := `<?php
use App\Http\Controllers\FooController;
use App\Http\Controllers\BarController as B;
use Some\Other\Namespace as Other ;
`
	got := parseUseStatements(src)
	want := map[string]string{
		"FooController": "App\\Http\\Controllers\\FooController",
		"B":             "App\\Http\\Controllers\\BarController",
		"Other":         "Some\\Other\\Namespace",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v\nwant %#v", got, want)
	}
}

func TestHTTPEntryPointID(t *testing.T) {
	cases := map[string]string{
		"POST /api/v2/refunds":        "ep.http.post.api.v2.refunds",
		"GET /users/{user}/edit":      "ep.http.get.users.user.edit",
		"GET /":                       "ep.http.get",
		"DELETE /api/posts/{post}":    "ep.http.delete.api.posts.post",
	}
	for in, want := range cases {
		parts := strings.SplitN(in, " ", 2)
		if got := httpEntryPointID(parts[0], parts[1]); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}
