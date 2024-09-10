import type { FunctionComponent } from "react";
import { Menu, MenuButton, MenuItem, MenuItems } from "@headlessui/react";

interface Version {
  version: string;
  slug: string;
}

interface Props {
  versions: Version[];
  currentIndex: number;
}

export const VersionSelector: FunctionComponent<Props> = ({
  versions,
  currentIndex,
}) => {
  return (
    <Menu>
      <MenuButton className="text-violet-300 border border-violet-300 rounded px-2 py-1 min-w-32 flex items-center justify-between [--rotate:0deg] data-[open]:[--rotate:180deg]">
        <span>
          {versions[currentIndex].version}
          {currentIndex === 0 ? (
            <span className="ml-1 text-violet-200">Latest</span>
          ) : (
            ""
          )}
        </span>
        <svg viewBox="0 0 10.54 9.4" className="scale-[0.35] w-8 rotate-[var(--rotate)] transition-transform">
          <path
            className="fill-violet-300"
            d="M7.14,12.2,2.86,4.8a1,1,0,0,1,.87-1.5h8.54a1,1,0,0,1,.87,1.5L8.86,12.2A1,1,0,0,1,7.14,12.2Z"
            transform="translate(-2.73 -3.3)"
          />
        </svg>
      </MenuButton>
      <MenuItems
        anchor="bottom"
        className="shadow-md mt-2 text-violet-300 border border-violet-300 rounded bg-violet-950 min-w-32"
      >
        {versions.map((version, index) => (
          <MenuItem
            key={version.slug}
            
          >
            <a href={import.meta.env.BASE_URL + version.slug} className="block py-1 px-2 hover:bg-violet-900 data-[focus]:bg-violet-900">
              {version.version}
              {index === 0 ? (
                <span className="ml-1 text-violet-200">Latest</span>
              ) : (
                ""
              )}
            </a>
          </MenuItem>
        ))}
      </MenuItems>
    </Menu>
  );
};
