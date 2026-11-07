create table tuples (
  object_type      text not null,
  object_id        text not null,
  relation         text not null,
  subject_type     text not null,
  subject_id       text not null,
  -- Empty rather than null for a single-object subject, since primary key columns can't be null.
  subject_relation text not null default '',
  -- The leading columns (object, relation) are exactly what a check looks up.
  primary key (object_type, object_id, relation, subject_type, subject_id, subject_relation)
);
-- A lookup walks backward, finding the tuples that name a subject.
create index tuples_by_subject on tuples (subject_type, subject_id);

-- One row counting every write and delete. Each change bumps it in the same
-- transaction, so seeing revision N means every change up to N is committed.
create table revision (
  only_row boolean primary key default true check (only_row),
  value    bigint not null
);
insert into revision (value) values (0);
