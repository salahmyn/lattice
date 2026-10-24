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

-- Keeps a row's updated_at current. No app code calls it: the database runs
-- it through the trigger below, which makes it reached.
-- @feature wallet
create or replace function public.touch_account()
returns trigger language plpgsql as $$
begin
  new.updated_at := now();
  return new;
end;
$$;

-- @feature wallet
create trigger accounts_touch before update on public.accounts
  for each row execute function public.touch_account();
