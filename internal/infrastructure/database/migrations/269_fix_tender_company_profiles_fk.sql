-- Fix tender_company_profiles.user_id to reference tender_users instead of users
ALTER TABLE tender_company_profiles DROP CONSTRAINT IF EXISTS tender_company_profiles_user_id_fkey;
ALTER TABLE tender_company_profiles ADD CONSTRAINT tender_company_profiles_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES tender_users(id) ON DELETE CASCADE;

-- Also fix tender_tenders.buyer_id to reference tender_users instead of tender_company_profiles
ALTER TABLE tender_tenders DROP CONSTRAINT IF EXISTS tender_tenders_buyer_id_fkey;
ALTER TABLE tender_tenders ADD CONSTRAINT tender_tenders_buyer_id_fkey
    FOREIGN KEY (buyer_id) REFERENCES tender_users(id) ON DELETE CASCADE;
