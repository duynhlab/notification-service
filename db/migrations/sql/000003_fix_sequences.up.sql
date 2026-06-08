-- V3__fix_sequences.sql
-- Fix sequence desynchronization caused by seed data inserting explicit ids.
-- Without this, the first application INSERT collides on the primary key.

-- Set the sequence for notifications table to the max id
SELECT setval('notifications_id_seq', (SELECT MAX(id) FROM notifications));
