alter table outbox add attempt_count int not null default 0;
alter table outbox add last_error text;
alter table outbox add id_target_worker text;
