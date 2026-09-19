alter table payment
add constraint payment_id_request_source_account_destiny_account unique (id_request, id_source_account, id_destiny_account);
