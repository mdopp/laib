"""a stand-in for the hooks image's bcrypt, for the tests: the same calls, a sha256 instead of the work"""
import hashlib, os


def gensalt(rounds=10):
    return b"$2b$%02d$" % rounds + os.urandom(8).hex().encode()


def hashpw(pw, salt):
    salt = salt[:23]
    return salt + hashlib.sha256(salt + pw).hexdigest().encode()


def checkpw(pw, hashed):
    return hashpw(pw, hashed[:23]) == hashed
