"""Reproducible standalone ClamAV build. No signature database downloads.

Requires CMake >= 3.17, Ninja, Perl, an Apple SDK and both Rust macOS targets.
Only Apple's system libraries may remain dynamically linked. The desktop Rust
crate must NEVER link any of these archives. Sources and Cargo vendor files are
packaged alongside the build recipe for the corresponding-source release asset.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import urllib.request

HERE = Path(__file__).resolve().parent


def run(args, cwd=None, env=None):
    print('+', ' '.join(map(str, args)), flush=True)
    subprocess.run(list(map(str, args)), cwd=cwd, env=env, check=True)


def fetch(root, spec):
    archive = root / 'sources' / spec['archive']
    archive.parent.mkdir(parents=True, exist_ok=True)
    if not archive.exists():
        part = archive.with_suffix(archive.suffix + '.part')
        with urllib.request.urlopen(spec['url'], timeout=120) as response, part.open('wb') as out:
            shutil.copyfileobj(response, out)
        part.rename(archive)
    if hashlib.sha256(archive.read_bytes()).hexdigest() != spec['sha256']:
        raise ValueError(f'Source checksum mismatch: {archive}')
    return archive


def unpack(archive, dest):
    dest.mkdir(parents=True, exist_ok=True)
    if not list(dest.iterdir()):
        with tarfile.open(archive) as src:
            # Upstream archives may contain relative links; reject escapes/devices.
            src.extractall(dest, filter='data')
    children = list(dest.iterdir())
    if len(children) != 1 or not children[0].is_dir():
        raise ValueError(f'Expected one source root in {archive}')
    return children[0]


def build_arch(root, arch, specs, jobs):
    work = root / arch
    prefix = work / 'prefix'
    prefix.mkdir(parents=True, exist_ok=True)
    sdk = subprocess.check_output(['xcrun', '--show-sdk-path'], text=True).strip()
    env = {**os.environ, 'MACOSX_DEPLOYMENT_TARGET': '13.0',
           # Rust 1.96 stripped build-time proc macros fail dyld on SDK 27.
           'CARGO_PROFILE_RELEASE_STRIP': 'none',
           'CARGO_PROFILE_RELEASE_BUILD_OVERRIDE_STRIP': 'none',
           'PKG_CONFIG_LIBDIR': str(prefix / 'lib/pkgconfig'),
           'PKG_CONFIG_PATH': str(prefix / 'lib/pkgconfig')}
    flags = f'-arch {arch} -mmacosx-version-min=13.0 -isysroot {sdk} -O2'
    env.update(CC='clang', CXX='clang++', CFLAGS=flags, CXXFLAGS=flags,
               LDFLAGS=f'-arch {arch} -L{prefix}/lib')

    def cmake(name, options, targets=None):
        src = unpack(fetch(root, specs[name]), work / 'src' / name)
        if name == 'clamav':
            # Every patch in patches/ is applied once per scratch tree, in name order.
            for patch in sorted((HERE / 'patches').glob('*.patch')):
                stamp = ('.opennavo-static-headers' if patch.name == 'static-unrar-headers.patch'
                         else f'.opennavo-patch-{patch.stem}')
                patch_stamp = src / stamp
                digest = hashlib.sha256(patch.read_bytes()).hexdigest()
                if not patch_stamp.exists():
                    run(['patch', '-p1', '-i', patch], cwd=src)
                    patch_stamp.write_text(digest)
                elif patch_stamp.read_text() != digest:
                    raise ValueError('Patch changed; rebuild using a fresh scratch directory')
        out = work / 'build' / name
        out.mkdir(parents=True, exist_ok=True)
        run(['cmake', '-S', src, '-B', out, '-G', 'Ninja',
             '-DCMAKE_BUILD_TYPE=Release', f'-DCMAKE_INSTALL_PREFIX={prefix}',
             f'-DCMAKE_PREFIX_PATH={prefix}', '-DCMAKE_IGNORE_PREFIX_PATH=/opt/homebrew;/usr/local',
             '-DCMAKE_OSX_DEPLOYMENT_TARGET=13.0', f'-DCMAKE_OSX_ARCHITECTURES={arch}',
             '-DBUILD_SHARED_LIBS=OFF', '-DCMAKE_POLICY_VERSION_MINIMUM=3.5', *options], cwd=out, env=env)
        run(['cmake', '--build', out, '--parallel', jobs, *(['--target', *targets] if targets else [])], env=env)
        if targets is None:
            run(['cmake', '--install', out], env=env)
        return src, out

    # Each stamp is tied to the complete locked source list and build recipe.
    # Bump this revision whenever dependency build options change. ClamAV-only
    # recipe changes need not rebuild OpenSSL and all seven dependencies.
    recipe = hashlib.sha256(b'dependency-recipe-v1' + (HERE / 'sources.lock.json').read_bytes()).hexdigest()
    stamp = work / 'dependencies.done'
    if not stamp.exists() or stamp.read_text() != recipe:
        src = unpack(fetch(root, specs['zlib']), work / 'src/zlib')
        run([src / 'configure', '--static', f'--prefix={prefix}'], cwd=src, env=env)
        run(['make', f'-j{jobs}'], cwd=src, env=env)
        run(['make', 'install'], cwd=src, env=env)
        src = unpack(fetch(root, specs['bzip2']), work / 'src/bzip2')
        run(['make', f'-j{jobs}', 'libbz2.a', f'CC=clang', f'CFLAGS={flags}'], cwd=src, env=env)
        shutil.copy2(src / 'libbz2.a', prefix / 'lib/libbz2.a')
        shutil.copy2(src / 'bzlib.h', prefix / 'include/bzlib.h')
        src = unpack(fetch(root, specs['openssl']), work / 'src/openssl')
        run(['perl', src / 'Configure', f'darwin64-{arch}-cc', 'no-shared', 'no-tests', 'no-module',
             f'--prefix={prefix}', '-mmacosx-version-min=13.0'], cwd=src, env=env)
        run(['make', f'-j{jobs}'], cwd=src, env=env)
        run(['make', 'install_sw'], cwd=src, env=env)
        cmake('libxml2', ['-DLIBXML2_WITH_PYTHON=OFF', '-DLIBXML2_WITH_TESTS=OFF',
                         '-DLIBXML2_WITH_PROGRAMS=OFF', '-DLIBXML2_WITH_LZMA=OFF',
                         '-DLIBXML2_WITH_ICONV=OFF', '-DLIBXML2_WITH_ZLIB=OFF'])
        cmake('pcre2', ['-DPCRE2_BUILD_TESTS=OFF', '-DPCRE2_BUILD_PCRE2GREP=OFF'])
        cmake('json-c', ['-DBUILD_TESTING=OFF', '-DBUILD_APPS=OFF'])
        cmake('curl', ['-DBUILD_CURL_EXE=OFF', '-DBUILD_TESTING=OFF', '-DBUILD_LIBCURL_DOCS=OFF',
                       # SDK 27 declares pipe2, but deployment target 13 cannot use it.
                       '-DHAVE_PIPE2=OFF',
                       '-DENABLE_CURL_MANUAL=OFF', '-DHTTP_ONLY=ON', '-DCURL_USE_OPENSSL=ON',
                       '-DOPENSSL_USE_STATIC_LIBS=TRUE', f'-DOPENSSL_ROOT_DIR={prefix}',
                       '-DCURL_USE_LIBPSL=OFF', '-DCURL_USE_LIBSSH2=OFF', '-DCURL_USE_LIBSSH=OFF',
                       '-DCURL_USE_LIBIDN2=OFF', '-DUSE_NGHTTP2=OFF', '-DCURL_BROTLI=OFF',
                       '-DCURL_ZSTD=OFF', '-DCURL_ZLIB=OFF', '-DCURL_USE_GSSAPI=OFF',
                       '-DCURL_USE_APPLE_IDN=OFF', '-DCURL_DISABLE_LDAP=ON', '-DCURL_DISABLE_LDAPS=ON'])
        stamp.write_text(recipe)
    triple = 'aarch64-apple-darwin' if arch == 'arm64' else 'x86_64-apple-darwin'
    src, out = cmake('clamav', [
        '-DENABLE_STATIC_LIB=ON', '-DENABLE_SHARED_LIB=OFF', '-DENABLE_UNRAR=OFF',
        '-DENABLE_TESTS=OFF', '-DENABLE_MAN_PAGES=OFF', '-DENABLE_EXAMPLES=OFF',
        '-DENABLE_MILTER=OFF', '-DENABLE_CLAMONACC=OFF', '-DENABLE_SYSTEMD=OFF',
        '-DENABLE_APP=ON', '-DAPP_CONFIG_DIRECTORY=etc', '-DDATABASE_DIRECTORY=share/clamav',
        '-DENABLE_MSPACK=ON', '-DOPENSSL_USE_STATIC_LIBS=TRUE', f'-DOPENSSL_ROOT_DIR={prefix}',
        f'-DZLIB_LIBRARY={prefix}/lib/libz.a', f'-DBZIP2_LIBRARY_RELEASE={prefix}/lib/libbz2.a',
        f'-DLIBXML2_LIBRARY={prefix}/lib/libxml2.a', f'-DCURL_LIBRARY_RELEASE={prefix}/lib/libcurl.a',
        f'-DRUST_COMPILER_TARGET={triple}',
    ], targets=['clamd', 'freshclam', 'clamscan'])
    dest = work / 'artifacts'
    dest.mkdir(exist_ok=True)
    for name in ['clamd', 'freshclam', 'clamscan']:
        shutil.copy2(out / name / name, dest / name)
    shutil.copytree(src / 'certs', dest / 'certs', dirs_exist_ok=True)
    return src, dest


def verify(binary, arches):
    actual = subprocess.check_output(['lipo', '-archs', str(binary)], text=True).split()
    if set(actual) != set(arches):
        raise ValueError(f'Wrong architectures: {binary}: {actual}')
    output = subprocess.check_output(['otool', '-L', binary], text=True)
    for line in output.splitlines():
        if line.startswith('\t'):
            library = line.strip().split(' (')[0]
            if not library.startswith(('/usr/lib/', '/System/Library/')):
                raise ValueError(f'Non-system dynamic dependency: {binary}: {library}')
    versions = subprocess.check_output(['vtool', '-show-build', binary], text=True)
    for line in versions.splitlines():
        if line.strip().startswith('minos ') and line.split()[-1] != '13.0':
            raise ValueError(f'Wrong minimum OS: {binary}: {line}')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--work', type=Path, required=True)
    parser.add_argument('--arch', choices=['arm64', 'x86_64', 'universal'], default='universal')
    parser.add_argument('--jobs', type=int, default=6)
    parser.add_argument('--source-bundle', action='store_true')
    args = parser.parse_args()
    root = args.work.resolve()
    root.mkdir(parents=True, exist_ok=True)
    specs = json.loads((HERE / 'sources.lock.json').read_text())
    arches = ['arm64', 'x86_64'] if args.arch == 'universal' else [args.arch]
    outputs = [build_arch(root, arch, specs, args.jobs) for arch in arches]
    dest = root / 'artifacts' / args.arch
    dest.mkdir(parents=True, exist_ok=True)
    for name in ['clamd', 'freshclam', 'clamscan']:
        if len(outputs) == 2:
            run(['lipo', '-create', *(d / name for _, d in outputs), '-output', dest / name])
        else:
            shutil.copy2(outputs[0][1] / name, dest / name)
        verify(dest / name, arches)
    shutil.copy2(outputs[0][0] / 'certs/clamav.crt', dest / 'clamav.crt')
    if args.source_bundle:
        src = outputs[0][0]
        # The official release tarball already contains the exact Cargo vendor
        # tree and config. The build above is --offline; keep that original tree.
        if not (src / '.cargo/vendor').is_dir() or not (src / '.cargo/config.toml').is_file():
            raise ValueError('Official source archive lacks offline Cargo sources')
        with tarfile.open(root / f'clamav-{specs["clamav"]["version"]}-corresponding-source.tar.gz', 'w:gz') as archive:
            for spec in specs.values():
                archive.add(fetch(root, spec), arcname='sources/' + spec['archive'])
            archive.add(HERE, arcname='recipe')
    print(f'Built standalone helpers in {dest}; signing/notarization is a separate step.')


if __name__ == '__main__':
    main()
