import { Outlet, Link } from 'react-router';
import { FaCog } from 'react-icons/fa';

function App() {
  return (
    <>
      <div className="flex flex-col min-h-screen text-brand font-display">
        <div>
          <div className="flex flex-row gap-4 my-4">
            {/* top bar */}
            <Link
              to={{
                pathname: '/',
              }}
            >
              <div className="flex flex-row gap-4 ml-8">
                <img src="logo.png" className="h-20 w-20" />
                <h1 className="m-auto text-6xl font-black">The RED Engine</h1>
              </div>
            </Link>
            <nav className="flex mx-64 text-3xl font-light gap-16">
              <Link
                to={{
                  pathname: '/articles',
                }}
                className="m-auto flex flex-col border-b-2 border-white hover:border-brand-400 hover:border-b-2 transition duration-300 ease-in-out"
              >
                Articles
              </Link>
              <Link
                to={{
                  pathname: '/network',
                }}
                className="m-auto flex flex-col border-b-2 border-white hover:border-brand-400 hover:border-b-2 transition duration-300 ease-in-out"
              >
                Network
              </Link>
              <Link
                to={{
                  pathname: '/about',
                }}
                className="m-auto flex flex-col border-b-2 border-white hover:border-brand-400 hover:border-b-2 transition duration-300 ease-in-out"
              >
                About
              </Link>
            </nav>
            <div className="flex flex-1">
              <FaCog className="ml-auto my-auto mr-8 text-4xl text-right justify-self-end self-end" />
            </div>
          </div>
        </div>
        <div className="flex flex-1 flex-row">
          <div className="flex flex-col border-r border-brand p-4">
            {/* Sidebar */}
            <p>Node Access</p>
            <div className="flex flex-row gap-4 align-middle justify-center"></div>
          </div>
          <div className="flex flex-1 flex-col">
            <Outlet />
            {/* For Children */}
          </div>
        </div>
      </div>
    </>
  );
}

export default App;
