-- NLI V20 Supabase metadata schema (RPC schema version 1).
-- Do not run this template directly. `sudo nli-controller-setup` renders an
-- installation-specific copy at /root/nli-supabase-bootstrap.sql.

begin;

create schema if not exists extensions;
create extension if not exists pgcrypto with schema extensions;

-- Private, purpose-separated Storage buckets. No anon/authenticated object
-- policies are created: only the controller's server-only secret key may use
-- the Storage API. Object paths are content-addressed and x-upsert is disabled.
insert into storage.buckets (id, name, public, file_size_limit, allowed_mime_types)
values
  ('nli-assurance-fixtures', 'nli-assurance-fixtures', false, 8388608, array['application/octet-stream']),
  ('nli-rollout-artifacts', 'nli-rollout-artifacts', false, 536870912, array['application/octet-stream','application/json','application/x-rpm']),
  ('nli-geoip-databases', 'nli-geoip-databases', false, 134217728, array['application/octet-stream'])
on conflict (id) do update set
  name = excluded.name,
  public = false,
  file_size_limit = excluded.file_size_limit,
  allowed_mime_types = excluded.allowed_mime_types;

create table if not exists public.nli_controller_settings (
  singleton boolean primary key default true check (singleton),
  secret_sha256 text not null check (secret_sha256 ~ '^[0-9a-f]{64}$'),
  schema_version integer not null default 1 check (schema_version = 1),
  updated_at timestamptz not null default now()
);

create table if not exists public.nli_documents (
  collection text not null check (collection ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$'),
  document_id text not null check (document_id ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$'),
  revision integer not null check (revision > 0),
  state text not null default '' check (length(state) <= 64),
  payload jsonb not null default '{}'::jsonb,
  sha256 text not null check (sha256 ~ '^[0-9a-f]{64}$'),
  created_by text not null default '' check (length(created_by) <= 128),
  created_at timestamptz not null,
  updated_at timestamptz not null,
  deleted boolean not null default false,
  primary key (collection, document_id, revision),
  constraint nli_documents_users_no_credentials check (
    collection <> 'users' or not (payload ?| array['password_hash','passwordhash','salt','jwt_secret','access_token','refresh_token']::text[])
  ),
  check (octet_length(payload::text) <= 4194304)
);

create index if not exists nli_documents_latest_idx
  on public.nli_documents (collection, document_id, revision desc);
create index if not exists nli_documents_list_idx
  on public.nli_documents (collection, state, updated_at desc);

alter table public.nli_controller_settings enable row level security;
alter table public.nli_documents enable row level security;
revoke all on table public.nli_controller_settings from public, anon, authenticated;
revoke all on table public.nli_documents from public, anon, authenticated;

insert into public.nli_controller_settings (singleton, secret_sha256, schema_version, updated_at)
values (true, '__NLI_CONTROLLER_SECRET_SHA256__', 1, now())
on conflict (singleton) do update
set secret_sha256 = excluded.secret_sha256,
    schema_version = excluded.schema_version,
    updated_at = excluded.updated_at;

create or replace function public.nli_controller_authorized()
returns boolean
language sql
stable
security definer
set search_path = pg_catalog, extensions, pg_temp
as $$
  select exists (
    select 1
    from public.nli_controller_settings
    where singleton
      and secret_sha256 = encode(
        digest(
          coalesce(nullif(current_setting('request.headers', true), '')::jsonb ->> 'x-nli-controller-secret', ''),
          'sha256'
        ),
        'hex'
      )
  );
$$;

create or replace function public.nli_health()
returns jsonb
language plpgsql
security definer
set search_path = pg_catalog, extensions, pg_temp
as $$
declare
  v_count bigint;
begin
  if not public.nli_controller_authorized() then
    raise exception using errcode = '42501', message = 'NLI controller authentication failed';
  end if;
  select count(*) into v_count from public.nli_documents;
  return jsonb_build_object('status', 'ok', 'schema_version', 1, 'revisions', v_count);
end;
$$;

create or replace function public.nli_put_document(
  p_collection text,
  p_id text,
  p_state text,
  p_payload jsonb,
  p_sha256 text,
  p_created_by text,
  p_deleted boolean default false
)
returns jsonb
language plpgsql
security definer
set search_path = pg_catalog, extensions, pg_temp
as $$
declare
  v_now timestamptz := now();
  v_revision integer;
  v_created_at timestamptz;
  v_created_by text;
  v_document public.nli_documents%rowtype;
begin
  if not public.nli_controller_authorized() then
    raise exception using errcode = '42501', message = 'NLI controller authentication failed';
  end if;
  if coalesce(p_collection, '') !~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$'
     or coalesce(p_id, '') !~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$' then
    raise exception using errcode = '22023', message = 'invalid collection or document id';
  end if;
  if coalesce(length(p_state), 0) > 64 or coalesce(length(p_created_by), 0) > 128
     or coalesce(p_sha256, '') !~ '^[0-9a-f]{64}$'
     or octet_length(coalesce(p_payload, '{}'::jsonb)::text) > 4194304 then
    raise exception using errcode = '22023', message = 'document metadata exceeds bounds';
  end if;

  perform pg_advisory_xact_lock(hashtextextended(p_collection || chr(31) || p_id, 0));
  select revision, created_at, created_by
    into v_revision, v_created_at, v_created_by
    from public.nli_documents
   where collection = p_collection and document_id = p_id
   order by revision desc
   limit 1;
  if not found then
    v_revision := 0;
    v_created_at := v_now;
    v_created_by := coalesce(p_created_by, '');
  elsif coalesce(p_created_by, '') <> '' then
    v_created_by := p_created_by;
  end if;

  insert into public.nli_documents (
    collection, document_id, revision, state, payload, sha256,
    created_by, created_at, updated_at, deleted
  ) values (
    p_collection, p_id, v_revision + 1, coalesce(p_state, ''),
    coalesce(p_payload, '{}'::jsonb), p_sha256, coalesce(v_created_by, ''),
    v_created_at, v_now, coalesce(p_deleted, false)
  ) returning * into v_document;

  return (to_jsonb(v_document) - 'document_id') || jsonb_build_object('id', v_document.document_id);
end;
$$;

create or replace function public.nli_get_document(
  p_collection text,
  p_id text,
  p_revision integer default 0
)
returns jsonb
language plpgsql
security definer
set search_path = pg_catalog, extensions, pg_temp
as $$
declare
  v_document public.nli_documents%rowtype;
begin
  if not public.nli_controller_authorized() then
    raise exception using errcode = '42501', message = 'NLI controller authentication failed';
  end if;
  if coalesce(p_collection, '') !~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$'
     or coalesce(p_id, '') !~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$' then
    raise exception using errcode = '22023', message = 'invalid collection or document id';
  end if;

  select * into v_document
    from public.nli_documents
   where collection = p_collection
     and document_id = p_id
     and (coalesce(p_revision, 0) <= 0 or revision = p_revision)
   order by revision desc
   limit 1;
  if not found or v_document.deleted then
    raise exception using errcode = 'P0002', message = 'document not found';
  end if;
  return (to_jsonb(v_document) - 'document_id') || jsonb_build_object('id', v_document.document_id);
end;
$$;

create or replace function public.nli_list_documents(
  p_collection text,
  p_state text default '',
  p_limit integer default 100,
  p_offset integer default 0
)
returns jsonb
language plpgsql
security definer
set search_path = pg_catalog, extensions, pg_temp
as $$
declare
  v_result jsonb;
begin
  if not public.nli_controller_authorized() then
    raise exception using errcode = '42501', message = 'NLI controller authentication failed';
  end if;
  if coalesce(p_collection, '') !~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$' then
    raise exception using errcode = '22023', message = 'invalid collection';
  end if;
  p_limit := least(greatest(coalesce(p_limit, 100), 1), 1000);
  p_offset := greatest(coalesce(p_offset, 0), 0);

  with latest as (
    select distinct on (document_id) *
      from public.nli_documents
     where collection = p_collection
     order by document_id, revision desc
  ), selected as (
    select * from latest
     where not deleted and (coalesce(p_state, '') = '' or state = p_state)
     order by updated_at desc, document_id
     limit p_limit offset p_offset
  )
  select coalesce(
    jsonb_agg((to_jsonb(selected) - 'document_id') || jsonb_build_object('id', selected.document_id) order by selected.updated_at desc, selected.document_id),
    '[]'::jsonb
  ) into v_result from selected;
  return v_result;
end;
$$;

create or replace function public.nli_delete_document(
  p_collection text,
  p_id text,
  p_purge boolean default false,
  p_actor text default ''
)
returns jsonb
language plpgsql
security definer
set search_path = pg_catalog, extensions, pg_temp
as $$
declare
  v_previous public.nli_documents%rowtype;
  v_now timestamptz := now();
begin
  if not public.nli_controller_authorized() then
    raise exception using errcode = '42501', message = 'NLI controller authentication failed';
  end if;
  if coalesce(p_collection, '') !~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$'
     or coalesce(p_id, '') !~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$' then
    raise exception using errcode = '22023', message = 'invalid collection or document id';
  end if;

  perform pg_advisory_xact_lock(hashtextextended(p_collection || chr(31) || p_id, 0));
  if coalesce(p_purge, false) then
    delete from public.nli_documents where collection = p_collection and document_id = p_id;
    return jsonb_build_object('status', 'purged');
  end if;

  select * into v_previous
    from public.nli_documents
   where collection = p_collection and document_id = p_id
   order by revision desc limit 1;
  if not found then
    raise exception using errcode = 'P0002', message = 'document not found';
  end if;
  insert into public.nli_documents (
    collection, document_id, revision, state, payload, sha256,
    created_by, created_at, updated_at, deleted
  ) values (
    p_collection, p_id, v_previous.revision + 1, 'deleted', '{}'::jsonb,
    encode(digest('{}', 'sha256'), 'hex'), coalesce(nullif(p_actor, ''), v_previous.created_by),
    v_previous.created_at, v_now, true
  );
  return jsonb_build_object('status', 'deleted');
end;
$$;

revoke all on function public.nli_controller_authorized() from public, anon, authenticated;
revoke all on function public.nli_health() from public, anon, authenticated;
revoke all on function public.nli_put_document(text,text,text,jsonb,text,text,boolean) from public, anon, authenticated;
revoke all on function public.nli_get_document(text,text,integer) from public, anon, authenticated;
revoke all on function public.nli_list_documents(text,text,integer,integer) from public, anon, authenticated;
revoke all on function public.nli_delete_document(text,text,boolean,text) from public, anon, authenticated;
grant execute on function public.nli_health() to anon;
grant execute on function public.nli_put_document(text,text,text,jsonb,text,text,boolean) to anon;
grant execute on function public.nli_get_document(text,text,integer) to anon;
grant execute on function public.nli_list_documents(text,text,integer,integer) to anon;
grant execute on function public.nli_delete_document(text,text,boolean,text) to anon;

comment on table public.nli_documents is
  'NLI V20 revisioned control-plane metadata. Assurance raw logs are AES-GCM encrypted by the controller before insertion.';

notify pgrst, 'reload schema';
commit;
