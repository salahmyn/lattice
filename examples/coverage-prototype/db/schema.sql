-- @feature wallet
create or replace function public.debit(account_id text, amount numeric)
returns void language plpgsql as $$
begin
  update accounts set balance = balance - amount where id = account_id;
end;
$$;

-- Superseded by public.debit. Nothing calls it: no app code, no trigger, no
-- other function, no cron job, no test.
-- @feature wallet
create or replace function public.debit_v1(account_id text, amount numeric)
returns void language plpgsql as $$
begin
  update accounts set balance = balance - amount where id = account_id;
end;
$$;
