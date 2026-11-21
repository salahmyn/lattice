-- @module-feature reports
select cron.schedule(job_name, '*/5 * * * *', $$ select app.do_x() $$);
