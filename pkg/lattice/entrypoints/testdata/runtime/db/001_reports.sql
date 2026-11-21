-- @module-feature reports
create function app.do_x() returns void language sql as $$ select 1; $$;

select cron.schedule('nightly-x', '0 3 * * *', $$ select app.do_x() $$);
