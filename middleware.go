// Copyright (c) 2022 Teal.Finance contributors
// This file is part of Teal.Finance/Incorruptible, a tiny cookie token.
// SPDX-License-Identifier: LGPL-3.0-or-later
// Teal.Finance/Incorruptible is free software under the GNU LGPL
// either version 3 or any later version, at the licensee's option.
// See the LICENSE file or <https://www.gnu.org/licenses/lgpl-3.0.html>

package incorruptible

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/teal-finance/incorruptible/dtoken"
)

// Set puts a "session" cookie when the request has no valid "incorruptible" token.
// The token is searched the "session" cookie and in the first "Authorization" header.
// The "session" cookie (that is added in the response) contains the "tiny" token.
// Finally, Set stores the decoded token in the request context.
func (s *Session) Set(next http.Handler) http.Handler {
	log.Printf("Middleware SessionSet cookie %q %v setIP=%v",
		s.cookie.Name, s.Expiry.Truncate(time.Second), s.SetIP)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dt, err := s.DecodeToken(r)
		if err != nil {
			printDebug("Set new token", err)
			// no valid token found => set a new token
			dt = s.SetCookie(w, r)
		}
		next.ServeHTTP(w, dt.PutInCtx(r))
	})
}

// Chk accepts requests only if it has a valid cookie.
// Chk does not verify the "Authorization" header.
// See the Vet() function to also verify the "Authorization" header.
// Chk also stores the decoded token in the request context.
// In dev. testing, Chk accepts any request but does not store invalid tokens.
func (s *Session) Chk(next http.Handler) http.Handler {
	log.Printf("Middleware SessionChk cookie DevMode=%v", s.IsDev)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dt, err := s.DecodeCookieToken(r)
		switch {
		case err == nil: // OK: put the token in the request context
			r = dt.PutInCtx(r)
		case s.IsDev:
			printDebug("Chk DevMode no cookie", err)
		default:
			s.writeErr(w, r, http.StatusUnauthorized, err.Error())
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Vet accepts requests having a valid token either in
// the "session" cookie or in the first "Authorization" header.
// Vet also stores the decoded token in the request context.
// In dev. testing, Vet accepts any request but does not store invalid tokens.
func (s *Session) Vet(next http.Handler) http.Handler {
	log.Printf("Middleware SessionVet cookie/bearer DevMode=%v", s.IsDev)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dt, err := s.DecodeToken(r)
		switch {
		case err == nil:
			r = dt.PutInCtx(r) // put the token in the request context
		case s.IsDev:
			printDebug("Vet DevMode no token", err)
		default:
			s.writeErr(w, r, http.StatusUnauthorized, err.Error())
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Session) DecodeToken(r *http.Request) (dtoken.DToken, error) {
	var dt dtoken.DToken
	var err [2]error
	var i int

	for i = 0; i < 2; i++ {
		var base91 string
		if i == 0 {
			base91, err[0] = s.CookieToken(r)
		} else {
			base91, err[1] = s.BearerToken(r)
		}
		if err[i] != nil {
			continue
		}
		if s.equalDefaultToken(base91) {
			return s.dtoken, nil
		}
		if dt, err[i] = s.Decode(base91); err[i] != nil {
			continue
		}
		if err[i] = dt.Valid(r); err[i] != nil {
			continue
		}
		break
	}

	if i == 2 {
		err[0] = fmt.Errorf("no valid 'incorruptible' token "+
			"in either in the %q cookie or in the first "+
			"'Authorization' HTTP header because: %w and %v",
			s.cookie.Name, err[0], err[1].Error())
		return dt, err[0]
	}

	return dt, nil
}

func (s *Session) DecodeCookieToken(r *http.Request) (dt dtoken.DToken, err error) {
	base91, err := s.CookieToken(r)
	if err != nil {
		return dt, err
	}
	if s.equalDefaultToken(base91) {
		return s.dtoken, nil
	}
	if dt, err = s.Decode(base91); err != nil {
		return dt, err
	}
	return dt, dt.Valid(r)
}

func (s *Session) DecodeBearerToken(r *http.Request) (dt dtoken.DToken, err error) {
	base91, err := s.BearerToken(r)
	if err != nil {
		return dt, err
	}
	if s.equalDefaultToken(base91) {
		return s.dtoken, nil
	}
	if dt, err = s.Decode(base91); err != nil {
		return dt, err
	}
	return dt, dt.Valid(r)
}

func (s *Session) CookieToken(r *http.Request) (base91 string, err error) {
	cookie, err := r.Cookie(s.cookie.Name)
	if err != nil {
		return "", err
	}

	// TODO: test if usable:
	// if !cookie.HttpOnly {
	// 	return "", errors.New("no HttpOnly cookie")
	// }
	// if cookie.SameSite != s.cookie.SameSite {
	// 	return "", fmt.Errorf("want cookie SameSite=%v but got %v", s.cookie.SameSite, cookie.SameSite)
	// }
	// if cookie.Secure != s.cookie.Secure {
	// 	return "", fmt.Errorf("want cookie Secure=%v but got %v", s.cookie.Secure, cookie.Secure)
	// }

	return trimTokenScheme(cookie.Value)
}

func (s *Session) BearerToken(r *http.Request) (base91 string, err error) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return "", errors.New("no 'Authorization: " + secretTokenScheme + "xxxxxxxx' in the request header")
	}

	return trimBearerScheme(auth)
}

// equalDefaultToken compares with the default token
// by skiping the token scheme.
func (s *Session) equalDefaultToken(base91 string) bool {
	const n = len(secretTokenScheme)
	return (base91 == s.cookie.Value[n:])
}

func trimTokenScheme(uri string) (base91 string, err error) {
	const n = len(secretTokenScheme)
	if len(uri) < n+base92MinSize {
		return "", fmt.Errorf("token URI too short (%d bytes) want %d", len(uri), n+base92MinSize)
	}
	if uri[:n] != secretTokenScheme {
		return "", fmt.Errorf("want token URI '"+secretTokenScheme+"xxxxxxxx' got %q", uri)
	}
	return uri[n:], nil
}

func trimBearerScheme(auth string) (base91 string, err error) {
	const n = len(prefixScheme)
	if len(auth) < n+base92MinSize {
		return "", fmt.Errorf("bearer too short (%d bytes) want %d", len(auth), n+base92MinSize)
	}
	if auth[:n] != prefixScheme {
		return "", fmt.Errorf("want '"+prefixScheme+"xxxxxxxx' got %s", auth)
	}
	return auth[n:], nil
}

func printDebug(str string, err error) {
	if doPrint {
		log.Printf("Session%s: %v", str, err)
	}
}
