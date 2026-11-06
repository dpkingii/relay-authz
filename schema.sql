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
