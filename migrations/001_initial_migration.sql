create table if not exists account(
    id_internal bigint generated always as identity primary key,
    id_external uuid not null unique,
    user_name text not null  
);

create table if not exists payment(
    id_internal bigint generated always as identity primary key,
    id_external uuid not null unique,
    id_request uuid not null unique,
    id_source_account bigint references account (id_internal),
    id_destiny_account bigint references account (id_internal),
    is_pending boolean not null,
    created_at timestamp with time zone not null,
    processed_at timestamp with time zone,
    id_psp_payment uuid,
    psp_result jsonb
);

create table if not exists outbox(
    id bigint generated always as identity primary key, 
    id_payment bigint references payment (id_internal),
    is_pending boolean not null,
    next_try_at timestamp with time zone not null,
    created_at timestamp with time zone not null,
    processed_at timestamp with time zone
);
