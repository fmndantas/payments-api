create table if not exists account(
    id_internal bigint generated always as identity primary key,
    id_external uuid not null unique,
    user_name text not null  
);

create table if not exists payment(
    id_internal bigint generated always as identity primary key,
    id_external uuid not null unique,
    id_request uuid not null unique,
    id_source_account bigint not null references account (id_internal),
    id_destiny_account bigint not null references account (id_internal),
    created_at timestamp with time zone not null,
    id_psp_payment uuid,
    psp_result jsonb
);

create table if not exists outbox(
    id bigint generated always as identity primary key, 
    id_payment bigint not null unique references payment (id_internal),
    status text not null default 'unprocessed',
    -- exponential backoff
    next_try_at timestamp with time zone not null,
    attempt_count int not null default 0,
    -- answers "may another worker claim this event now?"
    locked_until timestamp with time zone,
    -- answers "does the worker trying to update it still own the current claim?"
    lock_token uuid,
    created_at timestamp with time zone not null,
    last_processed_at timestamp with time zone,
    last_result text
);

insert into account(id_external, user_name) values
    ('e4215def-6f52-4f3a-8cd7-23e261bad9e7', 'Account 1'), 
    ('597cb0af-0562-496b-9802-94dc5b0f082d', 'Account 2');
