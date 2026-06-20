SELECT 'CREATE DATABASE flowspace_test OWNER flowspace'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'flowspace_test')\gexec

SELECT 'CREATE DATABASE flowspace_prod OWNER flowspace'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'flowspace_prod')\gexec
