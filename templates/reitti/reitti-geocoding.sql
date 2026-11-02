-- Reitti's geocoders send a place's coordinates to the service they name:
-- out of the box Paikka (geo.dedicatedcode.com) and Nominatim
-- (nominatim.openstreetmap.org). No location leaves the box, and there is no
-- geocoder on the box yet, so every one is off; post-deploy runs this on every
-- apply, also after an image update whose migration turns one back on.
UPDATE geocode_services SET enabled = false WHERE enabled;
SELECT 'reitti: Geocoder an: ' || count(*) FROM geocode_services WHERE enabled;
